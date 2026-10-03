# gev

`gev` is a Go CLI for decisions over stdin, files, and JSONL. It uses TypeSafe AI Jev; submitted content leaves your machine. Semantic judgments are not authorization or security proofs.

```sh
go build -o gev .
export TYPESAFE_API_KEY='...'
printf '%s' 'Login from a new country, followed by payout changes.' | ./gev eval --question 'takeover=This describes a plausible account takeover' --json
```

Set `TYPESAFE_API_KEY`, or store it in `~/.config/typesafe/credentials.env` (recommended file permissions: `600`):

```text
TYPESAFE_API_KEY='...'
```

`TYPESAFE_CREDENTIALS_FILE` overrides the file path. Never put credentials on the command line. By default the CLI calls `https://api.typesafe.ai/v1/systemone` with model `jev-latest`. For a local SystemOne-compatible endpoint (for example, one exposing a model named `nimble`):

```sh
./gev --api-url http://localhost:11434/v1/systemone --model nimble eval --question 'takeover=This describes a plausible account takeover' --text 'Login from a new country, followed by payout changes.' --json
```

`GEV_API_URL` and `GEV_MODEL` also work. Repeat `--question NAME=INSTRUCTIONS` to evaluate multiple named questions in one request; names must be unique, and commas or additional `=` characters inside instructions are preserved. `--text`, `--file`, or stdin supplies `state`. The CLI sends `{ "model": "nimble", "state": "...", "questions": { "takeover": { "type": "noul", "instructions": "..." } } }`. The server must support this request format and return `answers.takeover.noul`; changing the URL does not translate to Ollama's native `/api/generate` protocol. Custom endpoints do not require a TypeSafe key and are not sent one automatically. A JSON configuration file can be supplied with `--config path`; flags override config and environment values. Other controls include `--timeout` (seconds, default 10), `--retries` (default 2, maximum 5), `--max-input-bytes` (default 1,000,000), `--json`, and `--quiet`.

## Commands

The repository includes [fictional sample inputs and runnable commands](examples/README.md).

```sh
cat examples/incident.txt | ./gev eval --question 'security=This is a security incident' --question 'urgent=This needs urgent attention' --threshold 0.7 --uncertainty-margin 0.05 --json
cat examples/ticket.txt | ./gev choose --question 'Which queue?' --option support='ordinary support' --option security='security incident' --json
cat examples/answer.txt | ./gev score --question 'How complete is this answer?' --level 'incomplete' --level 'complete' --json
cat examples/tickets.jsonl | ./gev filter --question 'This needs urgent attention' --field text
./gev guard --action 'Delete production data' --context 'No exact approval' --json
```

`eval`, `choose`, `score`, and `filter` accept exactly one input source: stdin (default), `--text`, or `--file`. `choose`, `score`, and `filter` require one plain `--question`; `eval` accepts repeatable named `--question NAME=INSTRUCTIONS` flags. Stdin supplies the content being judged, not the question. `filter` reads JSONL objects, preserves order, and adds `_gev` metadata to matching records; `--raw` omits that metadata. `guard` accepts `--action` with optional `--context`, or a stdin JSON object with `action` and `context`. `check` is a deprecated alias for `guard`.

### Guard policies

`guard` uses an embedded Rego policy containing the original rule thresholds and order. Replace it for one invocation with a Rego v1 file:

```sh
./gev guard --action 'Delete production data' --context 'No approval exists' --policy examples/guard.rego --json
```

The [example policy](examples/guard.rego) returns `data.gev.guard.decision` as an object with exactly `route` (`allow`, `escalate`, or `block`) and a non-empty `reason`. The input is `input.signals`, containing `destructive`, `external_side_effect`, `secret_exposure`, `authorized`, `intent_clear` (probabilities from 0 to 1), `consequence` (score from 0 to 2), plus `jev_advisory` and `jev_advisory_confidence`. A custom file **replaces** the embedded policy, rather than merging with it. Missing, oversized, or invalid policy files return exit `2` before calling the provider; undefined or invalid policy results fail closed to `escalate` (exit `10`). Policy evaluation cannot use Rego's HTTP or DNS builtins. Only use policy files from trusted sources; semantic guard decisions are not authorization.

### Output policies

Every semantic command accepts `--output-policy path/to/output.rego`. This is separate from `guard --policy`, which controls the guard decision. The [example output policy](examples/output.rego) drops results containing `foo` from `eval` and `filter` while leaving `choose`, `score`, and `guard` unchanged:

```sh
cat tickets.jsonl | ./gev filter --question 'Urgent customer impact' --output-policy examples/output.rego
cat ticket.txt | ./gev eval --question 'foo=Some question' --question 'risk=Another question' --output-policy examples/output.rego --json
```

The policy receives `{ "command": "eval|choose|score|filter|guard", "data": ... }` as `input` and returns `data.gev.output.result`. `data` is the complete structured result, except for `filter`, where it is an array of all semantically selected records before JSONL output. Return the same shape to retain human, JSON, or JSONL rendering; if the shape changes, human output falls back to JSON. Returning `null` suppresses output. Output policies change **display only**—the original decision and exit code still apply, even if a guard route is hidden or rewritten. Invalid policy files return exit `2` before the provider call; a policy that errors or leaves `result` undefined emits no unfiltered data and returns exit `2`. Rego's HTTP and DNS builtins are unavailable. `--json` remains optional.

`eval --json` emits an ordered `results` array with each question, ID, verdict (`true`, `false`, or `uncertain`), probability, and threshold. All questions share one provider request. Its exit code is `0` when every result is definite (including `false`), `3` if any is uncertain, `2` for invalid input, or `4` for provider failure. Other commands use `0` definite success/match, `1` false/no filter matches, `2` invalid input, `3` uncertain, and `4` provider failure. `guard` instead uses `0` allow, `10` escalate (including provider failure), `20` block, and `2` invalid input. JSON errors go to stderr; `--quiet` emits no diagnostics. The machine output uses `schema_version: "1"`.

Run tests with `go test ./...`. No live API credentials are required for tests.

## Go library

The executable stays in the module root (`main.go`), and the reusable API is exported from `github.com/coderabbit-demo/gev/engine`. Cobra, Viper, file reading, rendering, and exit codes live in `cmd`; `engine` provides the TypeSafe client and typed `Eval`, `Choose`, `Score`, `Filter`, and `Guard` operations.

```go
import (
    "context"
    "time"

    "github.com/coderabbit-demo/gev/engine"
)

client := &engine.Client{
    URL: "https://api.typesafe.ai/v1/systemone",
    Model: "jev-latest",
    Timeout: 10 * time.Second,
    Retries: 2,
}
result, err := engine.Eval(context.Background(), client, "A new login followed by payout changes",
    []engine.NamedQuestion{{Name: "takeover", Instructions: "This describes account takeover"}},
    engine.PredicateOptions{Threshold: 0.70, UncertaintyMargin: 0.05})
// Inspect result.Results[0].Verdict and err.
```

The client reads `TYPESAFE_API_KEY` for the hosted endpoint. Library callers may supply their own `engine.Provider` for tests or alternative transports. Guard policies are compiled from source with `engine.CompileGuardPolicy(ctx, source)`; pass the resulting policy to `engine.Guard`. The CLI alone reads `--policy` files.

Library callers can also compile and apply output policies with `engine.CompileOutputPolicy(ctx, source)` and `policy.Apply(ctx, command, data)`.
