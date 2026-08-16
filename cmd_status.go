package main

// Subcommands that inspect running agent status across sessions: status, agent-status.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jskoll/wyrm/internal/agent"
	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/tmux"
)

type agentStatusPane struct {
	SessionID   string `json:"session_id"`
	SessionName string `json:"session_name"`
	WindowID    string `json:"window_id"`
	WindowName  string `json:"window_name"`
	WindowIndex int    `json:"window_index"`
	PaneID      string `json:"pane_id"`
	PaneIndex   int    `json:"pane_index"`
	Command     string `json:"command"`
	State       string `json:"state"`
}

type agentStatusSummary struct {
	Total   int `json:"total"`
	Blocked int `json:"blocked"`
	Idle    int `json:"idle"`
	Busy    int `json:"busy"`
}

type agentStatusReport struct {
	Summary agentStatusSummary `json:"summary"`
	Agents  []agentStatusPane  `json:"agents"`
}

func (a *app) status(args []string) error {
	fs := a.newFlagSet("status")
	format := fs.String("format", "text", "output format: text, json, tmux, waybar, sketchybar")
	sessionFilter := fs.String("session", "", "filter to a specific session by name or ID")
	verbose := fs.Bool("v", false, "verbose text output")
	watchLong := fs.Bool("watch", false, "continuously stream status output")
	watchShort := fs.Bool("w", false, "alias for -watch")
	interval := fs.Duration("interval", 2*time.Second, "polling interval in watch mode")

	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := requireNoArgs(fs); err != nil {
		return err
	}

	watch := *watchLong || *watchShort

	settings, _ := config.LoadSettings()
	var profiles []agent.Profile
	if settings != nil {
		configured := settings.AgentProfiles()
		if len(configured) == 0 {
			def := agent.DefaultProfile()
			if extra := settings.AgentCommands(); len(extra) > 0 {
				def.Commands = extra
			}
			profiles = []agent.Profile{def}
		} else {
			for i, p := range configured {
				compiled, err := agent.Profile{
					Commands:    p.Commands,
					Busy:        p.Busy,
					Blocked:     p.Blocked,
					Idle:        p.Idle,
					BusyPattern: p.BusyPattern,
				}.Compile()
				if err != nil {
					return fmt.Errorf("agent profile %d: %w", i, err)
				}
				profiles = append(profiles, compiled)
			}
		}
	} else {
		profiles = []agent.Profile{agent.DefaultProfile()}
	}

	if !watch {
		report, err := collectStatus(a.runner, *sessionFilter, profiles)
		if err != nil {
			return err
		}
		return formatStatus(a.stdout, *format, *verbose, report)
	}

	ctx := a.watchCtx
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
	}

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	var lastOut string
	first := true

	for {
		report, err := collectStatus(a.runner, *sessionFilter, profiles)
		if err == nil {
			var buf bytes.Buffer
			if err := formatStatus(&buf, *format, *verbose, report); err == nil {
				out := buf.String()
				if first || out != lastOut {
					first = false
					lastOut = out
					_, _ = fmt.Fprint(a.stdout, out)
				}
			}
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func collectStatus(runner tmux.Runner, sessionFilter string, profiles []agent.Profile) (agentStatusReport, error) {
	var report agentStatusReport
	report.Agents = make([]agentStatusPane, 0)

	refs, err := tmux.ListAllPanes(runner)
	if err != nil {
		return report, err
	}

	var candidates []tmux.PaneRef
	for _, ref := range refs {
		if sessionFilter != "" && ref.SessionName != sessionFilter && ref.SessionID != sessionFilter {
			continue
		}
		if agent.IsAgentPane(ref.Command, profiles) {
			candidates = append(candidates, ref)
		}
	}

	if len(candidates) > 0 {
		cmds := make([][]string, len(candidates))
		for i, ref := range candidates {
			cmds[i] = tmux.CapturePanePlainArgs(ref.PaneID)
		}
		contents := tmux.RunOutputs(runner, cmds)

		for i, ref := range candidates {
			if i >= len(contents) || contents[i] == "" {
				continue
			}
			st := agent.Detect(ref.Command, contents[i], profiles)
			if st == agent.StateNone || st == agent.StateUnknown {
				continue
			}
			report.Summary.Total++
			switch st {
			case agent.StateBlocked:
				report.Summary.Blocked++
			case agent.StateIdle:
				report.Summary.Idle++
			case agent.StateBusy:
				report.Summary.Busy++
			}
			report.Agents = append(report.Agents, agentStatusPane{
				SessionID:   ref.SessionID,
				SessionName: ref.SessionName,
				WindowID:    ref.WindowID,
				WindowName:  ref.WindowName,
				WindowIndex: ref.WindowIndex,
				PaneID:      ref.PaneID,
				PaneIndex:   ref.PaneIndex,
				Command:     ref.Command,
				State:       st.String(),
			})
		}
	}

	return report, nil
}

func formatStatus(w io.Writer, format string, verbose bool, report agentStatusReport) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)

	case "tmux":
		var parts []string
		if report.Summary.Blocked > 0 {
			parts = append(parts, fmt.Sprintf("#[fg=yellow,bold]⏸ %d blocked#[default]", report.Summary.Blocked))
		}
		if report.Summary.Idle > 0 {
			parts = append(parts, fmt.Sprintf("#[fg=cyan]✓ %d idle#[default]", report.Summary.Idle))
		}
		if len(parts) > 0 {
			_, _ = fmt.Fprintln(w, strings.Join(parts, " · "))
		}
		return nil

	case "waybar":
		type waybarOut struct {
			Text    string `json:"text"`
			Alt     string `json:"alt"`
			Tooltip string `json:"tooltip"`
			Class   string `json:"class"`
		}
		var wb waybarOut
		var parts []string
		var tooltips []string
		for _, ag := range report.Agents {
			tooltips = append(tooltips, fmt.Sprintf("%s: @%d:%s %s (%s)", ag.SessionName, ag.WindowIndex, ag.WindowName, ag.PaneID, ag.State))
		}
		if report.Summary.Blocked > 0 {
			parts = append(parts, fmt.Sprintf("⏸ %d", report.Summary.Blocked))
			wb.Class = "blocked"
			wb.Alt = "blocked"
		}
		if report.Summary.Idle > 0 {
			parts = append(parts, fmt.Sprintf("✓ %d", report.Summary.Idle))
			if wb.Class == "" {
				wb.Class = "idle"
				wb.Alt = "idle"
			}
		}
		if len(parts) == 0 {
			wb.Class = "none"
			wb.Alt = "none"
			wb.Tooltip = "No active agents"
		} else {
			wb.Text = strings.Join(parts, " · ")
			wb.Tooltip = strings.Join(tooltips, "\n")
		}
		return json.NewEncoder(w).Encode(wb)

	case "sketchybar":
		switch {
		case report.Summary.Blocked > 0:
			_, _ = fmt.Fprintf(w, "icon=⏸ label=\"%d blocked\" drawing=on\n", report.Summary.Blocked)
		case report.Summary.Idle > 0:
			_, _ = fmt.Fprintf(w, "icon=✓ label=\"%d idle\" drawing=on\n", report.Summary.Idle)
		default:
			_, _ = fmt.Fprintln(w, "drawing=off")
		}
		return nil

	case "text":
		if verbose {
			if len(report.Agents) == 0 {
				_, _ = fmt.Fprintln(w, "no active agents")
				return nil
			}
			for _, ag := range report.Agents {
				_, _ = fmt.Fprintf(w, "%s: @%d:%s %s (%s) - %s\n",
					ag.SessionName, ag.WindowIndex, ag.WindowName, ag.PaneID, ag.Command, ag.State)
			}
			return nil
		}
		var parts []string
		if report.Summary.Blocked > 0 {
			parts = append(parts, fmt.Sprintf("⏸ %d blocked", report.Summary.Blocked))
		}
		if report.Summary.Idle > 0 {
			parts = append(parts, fmt.Sprintf("✓ %d idle", report.Summary.Idle))
		}
		if len(parts) > 0 {
			_, _ = fmt.Fprintln(w, strings.Join(parts, " · "))
		}
		return nil

	default:
		return usageErrf("unknown format %q (want text, json, tmux, waybar, sketchybar)", format)
	}
}
