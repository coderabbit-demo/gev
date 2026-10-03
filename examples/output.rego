# Receives the complete structured result as input.data. Return the same shape
# to keep gev's normal human, JSON, and JSONL renderers.
package gev.output

result := object.union(input.data, {"results": [item |
    some item in input.data.results
    not contains(lower(json.marshal(item)), "foo")
]}) if {
    input.command == "eval"
} else := [record |
    some record in input.data
    not contains(lower(json.marshal(record)), "foo")
] if {
    input.command == "filter"
} else := input.data if {
    true
}
