package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/coderabbit-demo/gev/engine"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const schemaVersion = engine.SchemaVersion

type exitError struct {
	code    int
	kind    string
	message string
}

func (e *exitError) Error() string    { return e.message }
func inputError(message string) error { return &exitError{2, "input_error", message} }
func providerError() error {
	return &exitError{4, "provider_error", "semantic provider request failed"}
}

var rootCmd = newRoot()

type configContextKey struct{}

func commandConfig(cmd *cobra.Command) *viper.Viper {
	return cmd.Context().Value(configContextKey{}).(*viper.Viper)
}

func newRoot() *cobra.Command {
	v := viper.New()
	v.SetEnvPrefix("GEV")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()
	r := &cobra.Command{
		Use: "gev", Short: "Typed decisions for Unix pipelines and CI",
		SilenceUsage: true, SilenceErrors: true,
	}
	r.SetContext(context.WithValue(context.Background(), configContextKey{}, v))
	r.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error { return inputError(err.Error()) })
	rootFlags(r)
	r.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		bindRootFlags(cmd)
		if file := v.GetString("config"); file != "" {
			v.SetConfigFile(file)
			if err := v.ReadInConfig(); err != nil {
				return inputError("cannot read config file")
			}
		}
		return nil
	}
	r.AddCommand(newEval(), newChoose(), newScore(), newFilter(), newGuard("guard"), newGuard("check"))
	return r
}

func Execute() {
	err := rootCmd.Execute()
	if err == nil {
		return
	}
	var status *exitError
	if !errors.As(err, &status) {
		status = &exitError{2, "input_error", err.Error()}
	}
	args := os.Args[1:]
	if status.kind != "" && !hasArg(args, "--quiet") {
		command := ""
		for _, arg := range args {
			if arg == "eval" || arg == "choose" || arg == "score" || arg == "filter" || arg == "guard" || arg == "check" {
				command = arg
				break
			}
		}
		writeError(os.Stderr, command, hasArg(args, "--json"), status)
	}
	os.Exit(status.code)
}

func hasArg(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

func writeError(w io.Writer, command string, machine bool, status *exitError) {
	if machine {
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": schemaVersion, "command": command, "error": map[string]string{"kind": status.kind, "message": status.message}})
	} else {
		fmt.Fprintf(w, "gev: %s\n", status.message)
	}
}
