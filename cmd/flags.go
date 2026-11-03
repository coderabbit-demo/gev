// Copyright 2026 CodeRabbit
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import "github.com/spf13/cobra"

// Define flags during command construction, but bind them in pre-run. In
// particular, binding shared names like "threshold" during construction would
// let one subcommand's default override another subcommand's active flag.
func rootFlags(cmd *cobra.Command) {
	flags := cmd.PersistentFlags()
	flags.String("model", "jev-latest", "provider model name (GEV_MODEL)")
	flags.String("api-url", "https://api.typesafe.ai/v1/systemone", "SystemOne-compatible API URL (GEV_API_URL)")
	flags.Float64("timeout", 10, "per-attempt provider timeout in seconds")
	flags.Int("retries", 2, "transient retry count (0-5)")
	flags.Int("max-input-bytes", 1_000_000, "maximum UTF-8 input bytes")
	flags.Bool("json", false, "machine-readable output (JSONL for filter)")
	flags.Bool("quiet", false, "emit no output or diagnostics")
	flags.String("config", "", "optional JSON configuration file")
	flags.String("output-policy", "", "Rego policy file to filter the rendered result")
}

func bindRootFlags(cmd *cobra.Command) {
	v := commandConfig(cmd)
	for _, name := range []string{"model", "api-url", "timeout", "retries", "max-input-bytes", "json", "quiet", "config", "output-policy"} {
		_ = v.BindPFlag(name, cmd.Root().PersistentFlags().Lookup(name))
	}
}

func inputFlags(cmd *cobra.Command) {
	cmd.Flags().String("text", "", "input text instead of stdin")
	cmd.Flags().String("file", "", "read input from a UTF-8 file")
}

func bindInputFlags(cmd *cobra.Command) {
	v := commandConfig(cmd)
	_ = v.BindPFlag("text", cmd.Flags().Lookup("text"))
	_ = v.BindPFlag("file", cmd.Flags().Lookup("file"))
}

func imageFlags(cmd *cobra.Command) {
	cmd.Flags().StringArray("image", nil, "image file to include (repeat for multiple images)")
}

func bindImageFlags(cmd *cobra.Command) {
	_ = commandConfig(cmd).BindPFlag("image", cmd.Flags().Lookup("image"))
}

func evalFlags(cmd *cobra.Command) {
	inputFlags(cmd)
	imageFlags(cmd)
	cmd.Flags().StringArray("question", nil, "NAME=INSTRUCTIONS (repeat for multiple questions)")
	cmd.Flags().Float64("threshold", .70, "true probability threshold")
	cmd.Flags().Float64("uncertainty-margin", .05, "uncertain region around the threshold")
}

func bindEvalFlags(cmd *cobra.Command) {
	bindInputFlags(cmd)
	bindImageFlags(cmd)
	v := commandConfig(cmd)
	_ = v.BindPFlag("question", cmd.Flags().Lookup("question"))
	_ = v.BindPFlag("threshold", cmd.Flags().Lookup("threshold"))
	_ = v.BindPFlag("uncertainty-margin", cmd.Flags().Lookup("uncertainty-margin"))
}

func bindEvalCmdFlags(cmd *cobra.Command, _ []string) { bindEvalFlags(cmd) }

func chooseFlags(cmd *cobra.Command) {
	inputFlags(cmd)
	imageFlags(cmd)
	cmd.Flags().String("question", "", "question instructions")
	cmd.Flags().StringArray("option", nil, "NAME=DESCRIPTION (repeat for each option)")
	cmd.Flags().Float64("min-confidence", .50, "minimum confidence for a definite choice")
}

func bindChooseFlags(cmd *cobra.Command) {
	bindInputFlags(cmd)
	bindImageFlags(cmd)
	v := commandConfig(cmd)
	_ = v.BindPFlag("question", cmd.Flags().Lookup("question"))
	_ = v.BindPFlag("option", cmd.Flags().Lookup("option"))
	_ = v.BindPFlag("min-confidence", cmd.Flags().Lookup("min-confidence"))
}

func bindChooseCmdFlags(cmd *cobra.Command, _ []string) { bindChooseFlags(cmd) }

func scoreFlags(cmd *cobra.Command) {
	inputFlags(cmd)
	imageFlags(cmd)
	cmd.Flags().String("question", "", "question instructions")
	cmd.Flags().StringArray("level", nil, "ordered level description (repeat)")
	cmd.Flags().Float64("min-confidence", .50, "minimum confidence for a definite score")
}

func bindScoreFlags(cmd *cobra.Command) {
	bindInputFlags(cmd)
	bindImageFlags(cmd)
	v := commandConfig(cmd)
	_ = v.BindPFlag("question", cmd.Flags().Lookup("question"))
	_ = v.BindPFlag("level", cmd.Flags().Lookup("level"))
	_ = v.BindPFlag("min-confidence", cmd.Flags().Lookup("min-confidence"))
}

func bindScoreCmdFlags(cmd *cobra.Command, _ []string) { bindScoreFlags(cmd) }

func filterFlags(cmd *cobra.Command) {
	inputFlags(cmd)
	cmd.Flags().String("question", "", "question instructions for each record")
	cmd.Flags().String("field", "", "evaluate only this top-level record field")
	cmd.Flags().String("input-format", "jsonl", "input format: jsonl or csv (CSV requires a header row)")
	cmd.Flags().String("output-format", "jsonl", "output format: jsonl or csv")
	cmd.Flags().Float64("threshold", .70, "matching probability threshold")
	cmd.Flags().Float64("uncertainty-margin", .05, "uncertain region around the threshold")
	cmd.Flags().Int("max-records", 1000, "maximum input records")
	cmd.Flags().Bool("raw", false, "omit _gev metadata")
	cmd.Flags().Bool("jsonl", false, "emit JSONL (default)")
}

func bindFilterFlags(cmd *cobra.Command) {
	bindInputFlags(cmd)
	v := commandConfig(cmd)
	for _, name := range []string{"question", "field", "input-format", "output-format", "threshold", "uncertainty-margin", "max-records", "raw", "jsonl"} {
		_ = v.BindPFlag(name, cmd.Flags().Lookup(name))
	}
}

func bindFilterCmdFlags(cmd *cobra.Command, _ []string) { bindFilterFlags(cmd) }

func guardFlags(cmd *cobra.Command) {
	cmd.Flags().String("action", "", "proposed action (otherwise read JSON from stdin)")
	cmd.Flags().String("context", "", "authorization context")
	cmd.Flags().String("policy", "", "Rego policy file to replace the built-in guard policy")
}

func bindGuardFlags(cmd *cobra.Command) {
	v := commandConfig(cmd)
	_ = v.BindPFlag("action", cmd.Flags().Lookup("action"))
	_ = v.BindPFlag("context", cmd.Flags().Lookup("context"))
	_ = v.BindPFlag("policy", cmd.Flags().Lookup("policy"))
}

func bindGuardCmdFlags(cmd *cobra.Command, _ []string) { bindGuardFlags(cmd) }
