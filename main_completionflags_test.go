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

	// Each shell spells a flag differently, so each gets its own matcher
	// rather than a shared substring check that would pass on a coincidence.
	shells := []struct {
		path  string
		holds func(body, flag string) bool
	}{
		{"completions/wyrm.bash", func(body, flag string) bool {
			return regexp.MustCompile(`-` + regexp.QuoteMeta(flag) + `\b`).MatchString(body)
		}},
		{"completions/wyrm.fish", func(body, flag string) bool {
			return regexp.MustCompile(`-[ol] ` + regexp.QuoteMeta(flag) + `\b`).MatchString(body)
		}},
		{"completions/_wyrm", func(body, flag string) bool {
			return regexp.MustCompile(`'-` + regexp.QuoteMeta(flag) + `[\[:]`).MatchString(body)
		}},
	}

	for _, sh := range shells {
		body := read(sh.path)
		for verb, fl := range declared {
			// A verb absent from the file entirely is the subcommand tests'
			// business; this test only checks the flags of verbs that are
			// present, so one failure does not report as two.
			if !strings.Contains(body, verb) {
				continue
			}
			var missing []string
			for _, f := range fl {
				if !sh.holds(body, f) {
					missing = append(missing, "-"+f)
				}
			}
			if len(missing) > 0 {
				t.Errorf("%s: %q is missing %s", sh.path, verb, strings.Join(missing, " "))
			}
		}
	}
}
