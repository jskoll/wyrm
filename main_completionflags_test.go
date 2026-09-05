package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// declaredFlags returns every flag each verb registers, by parsing the
// cmd_*.go sources for the flag set a.newFlagSet("<verb>") returns.
//
// The subcommand tests already keep the completions' *verb* lists honest.
// Nothing kept their *flag* lists honest, and they had drifted: `wyrm up -d`
// and `wyrm restart -d` were missing from fish entirely, and zsh had no arm at
// all for send or setup-tmux, so tab-completion silently offered nothing for
// two shipped verbs.
func declaredFlags(t *testing.T) map[string][]string {
	t.Helper()
	sources, err := filepath.Glob("cmd_*.go")
	if err != nil {
		t.Fatal(err)
	}

	flags := map[string]map[string]bool{}
	for _, src := range sources {
		if strings.HasSuffix(src, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, src, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", src, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				return true
			}
			verb := ""
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				// a.newFlagSet("verb") names the verb the rest of this
				// function's flags belong to.
				if sel.Sel.Name == "newFlagSet" && len(call.Args) == 1 {
					if lit, ok := call.Args[0].(*ast.BasicLit); ok {
						verb = strings.Trim(lit.Value, `"`)
					}
					return true
				}
				if verb == "" {
					return true
				}
				// fs.Var(&dst, "x", ...) registers a custom flag.Value (e.g.
				// varMapFlag for --var) under its own method name, not a
				// "TypeVar" suffix of one of the typed constructors below —
				// it needs its own case or it falls through to "default:
				// return true" and disappears from the declared set
				// entirely, along with every completion check for it.
				if sel.Sel.Name == "Var" {
					if len(call.Args) <= 1 {
						return true
					}
					if lit, ok := call.Args[1].(*ast.BasicLit); ok {
						if flags[verb] == nil {
							flags[verb] = map[string]bool{}
						}
						flags[verb][strings.Trim(lit.Value, `"`)] = true
					}
					return true
				}
				// fs.Bool("x", ...) / fs.BoolVar(&x, "x", ...)
				name := strings.TrimSuffix(sel.Sel.Name, "Var")
				switch name {
				case "Bool", "String", "Int", "Duration", "Float64":
				default:
					return true
				}
				idx := 0
				if strings.HasSuffix(sel.Sel.Name, "Var") {
					idx = 1
				}
				if len(call.Args) <= idx {
					return true
				}
				lit, ok := call.Args[idx].(*ast.BasicLit)
				if !ok {
					return true
				}
				if flags[verb] == nil {
					flags[verb] = map[string]bool{}
				}
				flags[verb][strings.Trim(lit.Value, `"`)] = true
				return true
			})
			return true
		})
	}

	out := map[string][]string{}
	for verb, set := range flags {
		for f := range set {
			out[verb] = append(out[verb], f)
		}
		sort.Strings(out[verb])
	}
	return out
}

// bashVerbFlags maps each verb to the flags offered in its own case arm of
// _wyrm_complete's subcommand-specific switch, e.g.
// `restart) COMPREPLY=($(compgen -W "-config -n -d ..." -- "$cur")) ;;`.
// Verbs sharing an arm (`up|edit|validate)`) share its flag set.
func bashVerbFlags(body string) map[string]map[string]bool {
	blockRe := regexp.MustCompile(`(?m)^\s*([\w|-]+)\)\s*COMPREPLY=\(\$\(compgen -W "([^"]*)"`)
	out := map[string]map[string]bool{}
	for _, m := range blockRe.FindAllStringSubmatch(body, -1) {
		flags := map[string]bool{}
		for _, f := range strings.Fields(m[2]) {
			flags[f] = true
		}
		for _, verb := range strings.Split(m[1], "|") {
			out[verb] = flags
		}
	}
	return out
}

// fishVerbFlags maps each verb to the flags offered across every
// `complete -c wyrm -n '__fish_seen_subcommand_from V1 V2 ...' -o FLAG ...`
// line naming it — a verb's flags are scattered across as many such lines as
// it shares with other verbs, one flag per line.
func fishVerbFlags(body string) map[string]map[string]bool {
	lineRe := regexp.MustCompile(`__fish_seen_subcommand_from ([\w -]+)'\s+-o\s+(\S+)`)
	out := map[string]map[string]bool{}
	for _, m := range lineRe.FindAllStringSubmatch(body, -1) {
		flag := "-" + m[2]
		for _, verb := range strings.Fields(m[1]) {
			if out[verb] == nil {
				out[verb] = map[string]bool{}
			}
			out[verb][flag] = true
		}
	}
	return out
}

// zshVerbFlags maps each verb to the flags in its own arm of _wyrm's
// `case "$words[1]" in verb) _arguments '-flag[...]' ... ;; ... esac` block.
// Verbs sharing an arm (`edit|validate)`) share its flag set.
func zshVerbFlags(body string) map[string]map[string]bool {
	// Scope to the $words[1] switch specifically: the outer $state switch
	// (command)/args)) uses the same bare "label)" line style and would
	// otherwise be matched as a spurious block first, non-greedily
	// swallowing the real verb blocks that follow into its own capture
	// before they ever get a chance to be matched as their own headers.
	if i := strings.Index(body, `case "$words[1]" in`); i >= 0 {
		body = body[i:]
	}
	blockRe := regexp.MustCompile(`(?ms)^[ \t]*([\w][\w-]*(?:\|[\w][\w-]*)*)\)[ \t]*$(.*?);;`)
	flagRe := regexp.MustCompile(`'-([\w-]+)\[`)
	out := map[string]map[string]bool{}
	for _, m := range blockRe.FindAllStringSubmatch(body, -1) {
		flags := map[string]bool{}
		for _, fm := range flagRe.FindAllStringSubmatch(m[2], -1) {
			flags["-"+fm[1]] = true
		}
		for _, verb := range strings.Split(m[1], "|") {
			out[verb] = flags
		}
	}
	return out
}

// TestCompletionsCoverEveryFlag checks each verb's *own* completion arm for
// every flag it declares, not just whether the flag string occurs anywhere
// in the file. A shared substring check passes on a coincidence: "-n" is
// declared by several verbs, so it reads as present for all of them the
// moment any one of them has it, and a verb whose entire arm is missing (or
// whose flag is only ever offered by a *different* verb's arm) goes
// undetected either way.
func TestCompletionsCoverEveryFlag(t *testing.T) {
	declared := declaredFlags(t)
	if len(declared) == 0 {
		t.Fatal("parsed no flags from cmd_*.go — the parser, not the completions, is wrong")
	}

	read := func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	type shellCoverage struct {
		path    string
		body    string
		perVerb map[string]map[string]bool
	}
	bashBody := read("completions/wyrm.bash")
	fishBody := read("completions/wyrm.fish")
	zshBody := read("completions/_wyrm")
	shells := []shellCoverage{
		{"completions/wyrm.bash", bashBody, bashVerbFlags(bashBody)},
		{"completions/wyrm.fish", fishBody, fishVerbFlags(fishBody)},
		{"completions/_wyrm", zshBody, zshVerbFlags(zshBody)},
	}

	for _, sh := range shells {
		for verb, fl := range declared {
			// A verb absent from the file entirely is the subcommand tests'
			// business; this test only checks the flags of verbs that are
			// present, so one failure does not report as two.
			if !strings.Contains(sh.body, verb) {
				continue
			}
			// A verb present in the file but with no matching flag arm at
			// all looks up an empty set here — every one of its flags then
			// (correctly) reports missing, rather than being skipped the
			// way "absent from the file entirely" is above.
			set := sh.perVerb[verb]
			var missing []string
			for _, f := range fl {
				if !set["-"+f] {
					missing = append(missing, "-"+f)
				}
			}
			if len(missing) > 0 {
				t.Errorf("%s: %q is missing %s", sh.path, verb, strings.Join(missing, " "))
			}
		}
	}
}
