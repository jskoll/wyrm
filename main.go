// Command wyrm creates repeatable tmux session layouts from a TOML config.
package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/session"
	"github.com/jskoll/wyrm/internal/tmux"
)

// version is stamped at release time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	log.SetFlags(0)
	log.SetPrefix("wyrm: ")

	configPath := flag.String("config", "", "path to config file (default: .wyrm.toml, then .tmuxconfig)")
	kill := flag.Bool("kill", false, "kill the session (runs on_project_exit) instead of creating it")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("wyrm " + version)
		return
	}

	// Attaching from inside an existing tmux client would nest sessions,
	// which tmux handles poorly. -kill doesn't attach, so it's exempt.
	if !*kill && tmux.InsideTmux() {
		log.Fatal("cannot be run inside tmux; detach first (or use -kill, which works from inside tmux)")
	}

	path := *configPath
	var cfg *config.Config
	if path == "" {
		discovered, err := config.Discover()
		if err != nil {
			if cfg, err = config.LoadDefault(); err != nil {
				log.Fatal(err)
			}
		} else {
			path = discovered
		}
	}
	if cfg == nil {
		var err error
		if cfg, err = config.Load(path); err != nil {
			log.Fatal(err)
		}
	}

	runner := tmux.Exec{}

	if *kill {
		name, err := session.Kill(runner, cfg)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("killed session %s\n", name)
		return
	}

	name, reattached, err := session.Create(runner, cfg)
	if err != nil {
		log.Fatal(err)
	}
	if reattached {
		fmt.Printf("reattaching to existing session %s\n", name)
	} else {
		fmt.Printf("created session %s\n", name)
	}

	if err := tmux.Attach(name); err != nil {
		log.Fatalf("attaching to session: %v", err)
	}
}
