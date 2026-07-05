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

	path := *configPath
	if path == "" {
		var err error
		if path, err = config.Discover(); err != nil {
			log.Fatal(err)
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		log.Fatal(err)
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

	name, err := session.Create(runner, cfg)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("created session %s\n", name)

	// Inside an existing tmux client, attaching would nest — switch instead.
	if tmux.InsideTmux() {
		if out, err := runner.Run("switch-client", "-t", name); err != nil {
			log.Fatalf("switching to session: %v (%s)", err, out)
		}
		return
	}
	if err := tmux.Attach(name); err != nil {
		log.Fatalf("attaching to session: %v", err)
	}
}
