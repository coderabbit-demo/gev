# Example inputs

These files contain fictional data. Run the commands from the repository root.
For TypeSafe decisions, set `GEV_TOKEN`. You can also configure a compatible
endpoint. For hosted `extract`, set `GEV_CLOUDFLARE_ACCOUNT_ID` and `GEV_TOKEN`.

```sh
./gev eval --file examples/incident.txt \
  --question 'takeover=This describes plausible account takeover' \
  --question 'urgent=This requires immediate human attention' --json

./gev choose --file examples/ticket.txt \
  --question 'Which queue should receive this ticket?' \
  --option support='ordinary product support' \
  --option security='possible security incident' --json

./gev score --file examples/answer.txt \
  --question 'How complete is this password-reset answer?' \
  --level 'incorrect or misleading' \
  --level 'partially helpful' \
  --level 'correct and complete' --json

./gev filter --file examples/tickets.jsonl \
  --question 'The customer describes an urgent security or service incident' \
  --field text

./gev guard --json < examples/guard-action.json

./gev choose --image examples/truck.webp \
  --question 'What is this a picture of?' \
  --option truck='A pickup truck' \
  --option car='A car' \
  --option motorcycle='A motorcycle' \
  --option other='Something else' \
  --option semi='A semi truck'

# Extract simple typed fields from a text file.
./gev extract --file examples/invoice.txt \
  --field invoice_number=text \
  --field 'total=number,unit=USD,decimals=2,instructions=the amount due including shipping' \
  --field 'contacts=list:text,instructions=the invoice contacts'

# Use detailed field specifications for instructions, dates, and lists.
./gev extract --file examples/invoice.txt \
  --fields examples/invoice-fields.json --field vendor=text --json

# Send only an image. Clairic accepts WebP images.
./gev extract --image examples/truck.webp --field vehicle=text --json

# Send a PDF without text. Replace the path with your own PDF file.
./gev extract --document path/to/receipt.pdf \
  --field 'total=number,unit=USD,instructions=the receipt total' --json

# Remove contacts from the displayed result. This does not change the exit code.
./gev extract --file examples/invoice.txt \
  --fields examples/invoice-fields.json \
  --output-policy examples/extract-output.rego --json

```

Add `--output-policy examples/output.rego` to `eval` or `filter` to try the
[output policy](output.rego). It removes output items that contain `foo`.
Ticket 103 shows this behavior if the semantic filter selects it. Use
`--policy examples/guard.rego` with `guard` to try the separate guard policy.

The [extract output policy](extract-output.rego) removes the `contacts` answer
from the displayed output. It does not remove contacts from the provider
request. The [invoice field specifications](invoice-fields.json) show a JSON
file for `--fields`.

Repeat `--field NAME=TYPE` to add simple fields. Use `list:text`, `list:number`,
or `list:date` for lists. Put options before instructions. For example, use
`--field 'total=number,unit=USD,instructions=the amount due'`. Quote values
that contain spaces. Give `--fields` a file path, not a field definition.
Without `--json`, `extract` prints the name, value, and confidence for each field.
If Clairic does not return a confidence, the CLI prints `confidence=n/a`.

Semantic classifications use probabilities. Results can vary. The CLI sends
input to the configured provider. Do not use real secrets in examples.
