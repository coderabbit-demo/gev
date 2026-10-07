# Example inputs

These files contain fictional data. Run the commands from the repository root
after configuring a TypeSafe API key or a compatible local endpoint.

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

```

To try the [output policy](output.rego), add
`--output-policy examples/output.rego` to `eval` or `filter`. It removes
emitted items containing `foo`; ticket 103 is included to illustrate that
behavior if the semantic filter selects it. Use `--policy examples/guard.rego`
with `guard` to try the separate guard-decision policy.

Semantic classifications are probabilistic, so exact results can vary. Input
is sent to the configured provider; do not use real secrets in examples.
