# Allowed routes: allow (proceed), escalate (human review), block (refuse).
# Every decision must include a route and a non-empty reason.
# This file replaces the built-in policy when passed with --policy.

package gev.guard

decision := {"route": "block", "reason": "possible secret exposure"} if {
	input.signals.secret_exposure >= 0.70
} else := {"route": "allow", "reason": "explicitly authorized low-risk action"} if {
	input.signals.authorized >= 0.90
	input.signals.intent_clear >= 0.90
	input.signals.destructive < 0.25
	input.signals.external_side_effect < 0.35
	input.signals.secret_exposure < 0.25
	input.signals.consequence < 0.50
} else := {"route": "escalate", "reason": "manual review required"}
