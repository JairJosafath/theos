package model

// A Brief's planner sets a new Container's operations from the idea
// (plans/aws-production.md, step 2): only what the idea states, such as an
// email to alert or a recovery objective, and never a tier, which is the
// deploy target's. Left out, the factory's defaults apply. An existing
// Container's block is a person's to change, so model/model.yaml doesn't
// show it and a plan doesn't set it; the same block again, as a round applied
// once more after a restart writes it, is no change.

// checkOperations checks the plan's Containers' operations: set only on a
// Container the plan makes, and only to values the API server takes.
func checkOperations(plan Plan, current CurrentModel, refuse func(string, string, ...any)) {
	for _, o := range plan.Objects {
		if o.Kind != KindContainer || o.Operations == nil {
			continue
		}
		subject := o.Ref().String()
		if before, exists := findObject(current.Objects, o.Ref()); exists {
			if sameOperations(before.Operations, o.Operations) {
				continue
			}
			refuse(subject, "sets operations on a Container that exists; a plan writes operations only on a "+
				"Container it makes, so the block a person keeps isn't undone: leave operations out")
			continue
		}
		for _, problem := range OperationsProblems(*o.Operations) {
			refuse(subject, "operations.%s", problem)
		}
	}
}

// sameOperations reports whether a and b write the same block, field by
// field; an empty block is the same as none.
func sameOperations(a, b *OperationsChoices) bool {
	var x, y OperationsChoices
	if a != nil {
		x = *a
	}
	if b != nil {
		y = *b
	}
	days := func(d *int32) int32 {
		if d == nil {
			return -1
		}
		return *d
	}
	return days(x.RetentionDays) == days(y.RetentionDays) && x.CopyToRegion == y.CopyToRegion &&
		x.RPO == y.RPO && x.RTO == y.RTO && x.Email == y.Email
}
