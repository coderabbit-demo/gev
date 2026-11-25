# Hide contacts from the displayed extract result.
# The provider still receives the full input. This policy does not change the exit code.
package gev.output

import rego.v1

result := object.union(object.remove(input.data, ["answers"]), {"answers": object.remove(input.data.answers, ["contacts"])}) if {
    input.command == "extract"
} else := input.data if {
    true
}
