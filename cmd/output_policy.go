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

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// renderPolicyOutput preserves each command's usual renderer when the returned
// value retains its shape. Rewritten values fall back to JSON rather than being
// forced into an incompatible human-readable format.
func renderPolicyOutput(cmd *cobra.Command, command string, value any, machine bool) error {
	if command == "filter" {
		if records, ok := value.([]any); ok {
			for _, record := range records {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(record); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if !machine {
		if text, ok := policyHuman(command, value); ok {
			if text == "" {
				return nil
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), text)
			return err
		}
	}
	encoder := json.NewEncoder(cmd.OutOrStdout())
	if !machine {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(value)
}

func outputNumber(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	case json.Number:
		f, err := strconv.ParseFloat(string(n), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func policyHuman(command string, value any) (string, bool) {
	data, ok := value.(map[string]any)
	if !ok {
		return "", false
	}
	switch command {
	case "eval":
		items, ok := data["results"].([]any)
		if !ok {
			return "", false
		}
		lines := make([]string, 0, len(items))
		for _, item := range items {
			row, ok := item.(map[string]any)
			if !ok {
				return "", false
			}
			id, a := row["id"].(string)
			verdict, b := row["verdict"].(string)
			p, c := outputNumber(row["probability"])
			threshold, d := outputNumber(row["threshold"])
			if !a || !b || !c || !d {
				return "", false
			}
			lines = append(lines, fmt.Sprintf("%s %s probability=%.3f threshold=%.3f", id, strings.ToUpper(verdict), p, threshold))
		}
		return strings.Join(lines, "\n"), true
	case "choose":
		choice, a := data["choice"].(string)
		confidence, b := outputNumber(data["confidence"])
		uncertain, c := data["uncertain"].(bool)
		if !a || !b || !c {
			return "", false
		}
		suffix := ""
		if uncertain {
			suffix = " UNCERTAIN"
		}
		return fmt.Sprintf("%s confidence=%.3f%s", choice, confidence, suffix), true
	case "score":
		score, a := outputNumber(data["score"])
		confidence, b := outputNumber(data["confidence"])
		uncertain, c := data["uncertain"].(bool)
		if !a || !b || !c {
			return "", false
		}
		suffix := ""
		if uncertain {
			suffix = " UNCERTAIN"
		}
		return fmt.Sprintf("score=%.3f confidence=%.3f%s", score, confidence, suffix), true
	case "guard":
		route, a := data["route"].(string)
		reason, b := data["reason"].(string)
		signals, c := data["signals"].(map[string]any)
		if !a || !b || !c {
			return "", false
		}
		text := strings.ToUpper(route) + ": " + reason
		for _, key := range []string{"destructive", "external_side_effect", "secret_exposure", "authorized", "intent_clear", "consequence", "jev_advisory", "jev_advisory_confidence"} {
			if val, ok := signals[key]; ok && val != nil {
				text += fmt.Sprintf("\n%s=%v", key, val)
			}
		}
		return text, true
	}
	return "", false
}
