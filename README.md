# gev

`gev` is a Go CLI. It reads stdin, files, and JSONL for decisions. It uses TypeSafe AI Jev for decisions. It uses Cloudflare Clairic for extraction. The CLI sends input outside your machine. Semantic decisions do not prove authorization or security.

```sh
go build -o gev .
export GEV_TOKEN='...'
printf '%s' 'Login from a new country, followed by payout changes.' | ./gev eval --question 'takeover=This describes a plausible account takeover' --json
```

Set `GEV_TOKEN` for TypeSafe. Or put a `TYPESAFE_API_KEY` entry in `~/.config/typesafe/credentials.env`. Set the file permissions to `600`:

```text
TYPESAFE_API_KEY='...'
```

Set `GEV_TYPESAFE_CREDENTIALS_FILE` to use a different file. If `GEV_TOKEN` is not set, TypeSafe also accepts `GEV_TYPESAFE_API_KEY`, `TYPESAFE_API_KEY`, and `TYPESAFE_CREDENTIALS_FILE`. Do not put credentials on the command line.

By default, the CLI calls `https://api.typesafe.ai/v1/systemone`. It uses model `jev-latest`. You can use a local SystemOne-compatible endpoint instead. This example uses a model named `nimble`:

```sh
./gev --api-url http://localhost:11434/v1/systemone --model nimble eval --question 'takeover=This describes a plausible account takeover' --text 'Login from a new country, followed by payout changes.' --json
```

You can also set `GEV_API_URL` and `GEV_MODEL`. Repeat `--question NAME=INSTRUCTIONS` to send more than one named question. Each name must be unique. Instructions can contain commas and more `=` characters. Use `--text`, `--file`, or stdin to set `state`.

The CLI sends `{ "model": "nimble", "state": "...", "questions": { "takeover": { "type": "noul", "instructions": "..." } } }`. The server must accept this format. It must return `answers.takeover.noul`. A different URL does not add support for Ollama's native `/api/generate` protocol.

If you set `GEV_TOKEN`, the CLI sends it as a bearer token to a custom endpoint. The CLI does not send TypeSafe-specific fallback credentials to a custom endpoint. Check the custom URL before you set `GEV_TOKEN`. Use `--config path` to supply a JSON configuration file. Flags take precedence over configuration and environment values. You can also use `--timeout` (seconds, default 10), `--retries` (default 2, maximum 5), `--max-input-bytes` (default 1,000,000), `--json`, and `--quiet`.

## Commands

See [fictional sample inputs and commands](examples/README.md).

```sh
cat examples/incident.txt | ./gev eval --question 'security=This is a security incident' --question 'urgent=This needs urgent attention' --threshold 0.7 --uncertainty-margin 0.05 --json
cat examples/ticket.txt | ./gev choose --question 'Which queue?' --option support='ordinary support' --option security='security incident' --json
cat examples/answer.txt | ./gev score --question 'How complete is this answer?' --level 'incomplete' --level 'complete' --json
cat examples/tickets.jsonl | ./gev filter --question 'This needs urgent attention' --field text
./gev guard --action 'Delete production data' --context 'No exact approval' --json
```

### Decision inputs

`eval`, `choose`, `score`, and `filter` use one text source. Use stdin (the default), `--text`, or `--file`. For `eval`, `choose`, and `score`, repeat `--image path/to/image.png` to add images. These commands accept PNG, JPEG, and WebP files. Images can use up to 10 MiB in total. The CLI encodes each file as base64. It puts the files in the top-level `images` array in flag order.

You can send images with or without text. Without text, the CLI sets `state` to `{}`. Without images, you must supply text. For example, use `./gev --api-url http://localhost:11434/v1/systemone --model clef-flash eval --image photo.png --question 'damaged=Is the item damaged?'` with a local Ollama Clef Flash model. Image support requires a vision-capable model and a compatible API endpoint. The default `jev-latest` endpoint might not support images. Use `extract` for Cloudflare's hosted Clairic endpoint.

`choose`, `score`, and `filter` require one plain `--question`. `eval` accepts repeatable `--question NAME=INSTRUCTIONS` flags. Stdin supplies the content for the decision. It does not supply the question.

### Filter and guard

By default, `filter` reads JSONL objects. Use `--input-format csv` to read CSV with a header row. CSV cells remain strings. For example, use `./gev filter --file tickets.csv --input-format csv --output-format csv --question 'Urgent customer impact' --field text`. Both input formats keep record order.

By default, `filter` writes JSONL. Use `--output-format csv` to write CSV with a header row. CSV output keeps the order of CSV input columns. It sorts columns from JSONL input. Matching records include `_gev` metadata. CSV output puts this metadata in a JSON cell. Use `--raw` to omit the metadata. If no records match, `filter` writes no output.

Use `guard --action` to supply an action. You can also add `--context`. Or send a JSON object with `action` and `context` through stdin. `check` is a deprecated alias for `guard`.

### Extract typed fields with Clairic

`extract` calls [Cloudflare Clairic](https://developers.cloudflare.com/workers-ai/models/clairic/). It does not change the TypeSafe decision commands. Set `GEV_CLOUDFLARE_ACCOUNT_ID` and `GEV_TOKEN` to use Cloudflare. The CLI sends the input to Cloudflare.

```sh
export GEV_CLOUDFLARE_ACCOUNT_ID='...'
export GEV_TOKEN='...'
./gev extract --text 'Invoice INV-2291, total $1080.' --field invoice_number=text --field total=number --json
```

Repeat `--field NAME=TYPE` to add fields. The CLI accepts `text`, `number`, `compute`, `date`, `time`, `datetime`, `duration`, and `boolean`. For lists, use `list:text`, `list:number`, or `list:date`. Put options after the type. Put `instructions` last. Quote the full value if it contains spaces. Instructions can contain commas and `=` characters:

```sh
./gev extract --image receipt.png \
  --field 'total=number,unit=USD,decimals=2,instructions=the receipt total' \
  --field 'items=list:text,max_items=20,instructions=the items purchased' --json
```

The short form supports these options:

| Field type | Options |
| --- | --- |
| Text | `minLength`, `maxLength`, `nullable`, `knowledge` |
| Number | `minimum`, `maximum`, `unit`, `integer`, `decimals`, `nullable`, `knowledge`, `source_only` |
| Compute | `unit`, `integer`, `decimals`, `null_threshold` |
| Duration | `unit`, `integer`, `decimals`, `null_threshold` |
| Date, time, datetime | `source_only`, `null_threshold` |
| Boolean | `nullable` |
| List | `max_items`; use `knowledge` for text and number lists; use `source_only` for number and date lists |
| Number list | `unit`, `integer`, `decimals` |

Set boolean options to `true` or `false`. The CLI rejects invalid options before it sends a request. It also rejects conflicting options. Use `--fields path.json` for nested fields, enums, estimates, forecasts, and patterns. You can also use it for other detailed Clairic settings. The file must contain a JSON object. Map each field name to a field specification:

```json
{
  "total": {"type": "number", "decimals": 2, "unit": "USD", "instructions": "Include shipping"},
  "contacts": {"type": "list", "items": "text"}
}
```

You can use `--field` and `--fields` together. Give each field a different name. The CLI rejects duplicate names before it sends a request. Supply text through stdin, `--text`, or `--file`.

You can repeat `--image` up to four times. Use PNG, JPEG, or WebP files. Each image must be at most 4 MiB. Together, the images must be at most 8 MiB. You can send an image without text.

Repeat `--document path/to/receipt.pdf` to send PDFs. This flag sends each PDF in the Clairic `documents` field. Do not pass PDFs to `--image`. You can send a PDF without text. You can also combine PDFs, images, and text. Clairic accepts at most four PDFs. Each PDF must be at most 4 MiB. Images and PDFs together must be at most 8 MiB. Clairic accepts at most four rendered pages and images combined. The CLI checks the number of files and images. Cloudflare checks the rendered PDF page count and rejects encrypted PDFs.

```sh
./gev extract --document path/to/receipt.pdf \
  --field 'total=number,unit=USD,instructions=the receipt total' --json
```

By default, `extract` prints one line per field. It sorts fields by name. Each line shows the field name, its value, and its confidence:

```text
invoice_number="INV-2291" confidence=1.000
po_number=null confidence=n/a
total=1080 confidence=0.999
```

The CLI writes string values as JSON strings. It writes lists and objects as compact JSON. If an answer has no `confidence`, the CLI writes `confidence=n/a`. Use `extract --json` for the full typed `answers`, `model`, `truncated`, and `usage` data. If the input does not determine an answer, its `value` can be null. A complete response returns exit code `0`. This includes responses with null answers. If `truncated` is true, the command returns `3`. Invalid input returns `2`. A provider failure returns `4`. Use `--output-policy` to filter the result before display.

By default, `extract` uses model `clairic` and Cloudflare's Workers AI REST endpoint. Set `--api-url` or `GEV_API_URL` to use a custom endpoint. That endpoint must accept the same Clairic request and response format. If you set `GEV_TOKEN`, the CLI sends it as a bearer token to the custom endpoint. A custom URL does not add support for Ollama's native protocol. For this command, `--model` accepts `clairic` or `@cf/cloudflare/clairic`. The Cloudflare endpoint requires HTTPS.

### Guard policies

`guard` uses an embedded Rego policy. This policy keeps the original rule thresholds and order. Use a Rego v1 file to replace it for one command:

```sh
./gev guard --action 'Delete production data' --context 'No approval exists' --policy examples/guard.rego --json
```

The [example policy](examples/guard.rego) returns an object at `data.gev.guard.decision`. The object has a `route` and a non-empty `reason`. The `route` is `allow`, `escalate`, or `block`.

The policy reads `input.signals`. It contains these probabilities from 0 to 1: `destructive`, `external_side_effect`, `secret_exposure`, `authorized`, and `intent_clear`. It also contains `consequence` (a score from 0 to 2), `jev_advisory`, and `jev_advisory_confidence`.

A custom file **replaces** the embedded policy. It does not merge with the embedded policy. The CLI returns exit code `2` before it calls the provider if the file is missing, too large, or invalid. If the policy result is undefined or invalid, `guard` defaults to `escalate` (exit `10`). The policy cannot use Rego's HTTP or DNS builtins. Use only policy files from trusted sources. Semantic guard decisions do not prove authorization.

### Output policies

Every semantic command accepts `--output-policy path/to/output.rego`. This policy controls the displayed output. `guard --policy` controls the guard decision instead. The [example output policy](examples/output.rego) removes results that contain `foo` from `eval` and `filter`. It does not change `choose`, `score`, or `guard`:

```sh
cat tickets.jsonl | ./gev filter --question 'Urgent customer impact' --output-policy examples/output.rego
cat ticket.txt | ./gev eval --question 'foo=Some question' --question 'risk=Another question' --output-policy examples/output.rego --json
```

The policy receives `{ "command": "eval|choose|score|filter|extract|guard", "data": ... }` as `input`. It returns `data.gev.output.result`. For most commands, `data` is the complete structured result. For `filter`, it is an array of selected records. The CLI makes this array before it writes JSONL.

Return the same shape to keep the usual human, JSON, or JSONL output. If you return a different shape, human output uses JSON. Return `null` to suppress output. Output policies change **display only**. They do not change the decision or exit code. This also applies if a policy hides or changes a guard route.

The CLI returns exit code `2` before it calls the provider if a policy file is invalid. If the policy fails or leaves `result` undefined, the CLI returns `2`. It does not show unfiltered data. The policy cannot use Rego's HTTP or DNS builtins. `--json` remains optional.

`eval --json` writes an ordered `results` array. Each item has a question, ID, verdict (`true`, `false`, or `uncertain`), probability, and threshold. All questions use one provider request. `eval` returns `0` when all results are definite. This includes `false` results. It returns `3` if any result is uncertain, `2` for invalid input, and `4` for provider failure.

Other decision commands return `0` for definite success or a match. They return `1` for false or no filter matches. They return `2` for invalid input, `3` for uncertainty, and `4` for provider failure. `guard` uses different codes: `0` for allow, `10` for escalate (including provider failure), `20` for block, and `2` for invalid input. JSON errors go to stderr. `--quiet` stops diagnostic output. Machine output uses `schema_version: "1"`.

Run tests with `go test ./...`. You do not need live API credentials for tests.

## Go library

The executable is in the module root (`main.go`). The reusable API is in `github.com/coderabbit-demo/gev/engine`. The `cmd` package handles Cobra, Viper, file reading, output, and exit codes. Use `engine.Client` for both decisions and extraction. The `engine` package also provides typed `Eval`, `Choose`, `Score`, `Filter`, and `Guard` operations.

```go
import (
    "context"
    "encoding/json"
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

extractClient := &engine.Client{
    URL: "https://api.cloudflare.com/client/v4/accounts/ACCOUNT_ID/ai/run/@cf/cloudflare/clairic",
    Model: "clairic",
    Timeout: 10 * time.Second,
    Retries: 2,
}
extracted, err := extractClient.Extract(context.Background(), engine.ExtractionRequest{
    State: "Invoice INV-2291, total $1080",
    Fields: map[string]json.RawMessage{
        "total": json.RawMessage(`{"type":"number","unit":"USD"}`),
    },
})
// Inspect extracted.Answers["total"] and err.
```

If `APIKey` is empty, the client reads `GEV_TOKEN` for any endpoint. It uses TypeSafe-specific credentials as a fallback for the TypeSafe host only. Library callers can pass base64-encoded images to `engine.Eval`, `engine.Choose`, and `engine.Score`. Pass them as optional trailing arguments. Image-capable providers implement `EvaluateWithImages` in addition to `engine.Provider`. Library callers can supply their own `engine.Provider` for tests or other transports.

Use `engine.CompileGuardPolicy(ctx, source)` to compile a guard policy from source. Pass the compiled policy to `engine.Guard`. Only the CLI reads `--policy` files.

Configure `engine.Client` with a Clairic model and compatible URL before you call `Extract`. A TypeSafe model or endpoint cannot extract fields. Pass an `engine.ExtractionRequest` with text and a map of field specifications. You can also pass image data URLs in `Images` and PDF data URLs in `Documents`. Set `Client.APIKey` to override `GEV_TOKEN`. The `engine.Extractor` interface lets callers supply a different implementation. `engine.CloudflareExtractor` remains available as a direct adapter.

Library callers can compile an output policy with `engine.CompileOutputPolicy(ctx, source)`. Apply it with `policy.Apply(ctx, command, data)`.
