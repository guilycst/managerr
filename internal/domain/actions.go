package domain

import "fmt"

// ActionState is the durable state of one independently executable action.
type ActionState string

const (
	ActionQueued            ActionState = "queued"
	ActionRunning           ActionState = "running"
	ActionWaitingDependency ActionState = "waiting_dependency"
	ActionReconciling       ActionState = "reconciling"
	ActionNeedsReview       ActionState = "needs_review"
	ActionSucceeded         ActionState = "succeeded"
	ActionFailed            ActionState = "failed"
	ActionCancelled         ActionState = "cancelled"
	ActionDeadlineExceeded  ActionState = "deadline_exceeded"
)

// WorkflowState is the aggregate state of an ordered recipe.
type WorkflowState string

const (
	WorkflowAwaitingApproval  WorkflowState = "awaiting_approval"
	WorkflowRunning           WorkflowState = "running"
	WorkflowWaitingDependency WorkflowState = "waiting_dependency"
	WorkflowNeedsReview       WorkflowState = "needs_review"
	WorkflowSucceeded         WorkflowState = "succeeded"
	WorkflowFailed            WorkflowState = "failed"
	WorkflowCancelled         WorkflowState = "cancelled"
	WorkflowDeadlineExceeded  WorkflowState = "deadline_exceeded"
)

// StepState is the state of a workflow step, independent from the action's
// attempt and effect evidence.
type StepState string

const (
	StepQueued    StepState = "queued"
	StepRunning   StepState = "running"
	StepSucceeded StepState = "succeeded"
	StepFailed    StepState = "failed"
	StepBlocked   StepState = "blocked"
	StepSkipped   StepState = "skipped"
	StepCancelled StepState = "cancelled"
)

// AttemptState records the write/reconciliation phase of one dispatch.
type AttemptState string

const (
	AttemptRunning     AttemptState = "running"
	AttemptReconciling AttemptState = "reconciling"
	AttemptSucceeded   AttemptState = "succeeded"
	AttemptFailed      AttemptState = "failed"
	AttemptCancelled   AttemptState = "cancelled"
)

// ActionKind is the closed set of typed actions available in v0.0.1.
type ActionKind string

const (
	ActionArrRegistration  ActionKind = "arr.registration"
	ActionArrImport        ActionKind = "arr.import"
	ActionFSCopy           ActionKind = "fs.copy"
	ActionFSHardlink       ActionKind = "fs.hardlink"
	ActionFSMove           ActionKind = "fs.move"
	ActionFSRename         ActionKind = "fs.rename"
	ActionClientStop       ActionKind = "client.stop"
	ActionClientRemove     ActionKind = "client.remove"
	ActionFSTrash          ActionKind = "fs.trash"
	ActionFSRestore        ActionKind = "fs.restore"
	ActionFSDelete         ActionKind = "fs.delete"
	ActionDescriptorDelete ActionKind = "descriptor.delete"
	ActionJellyfinRefresh  ActionKind = "jellyfin.refresh"
)

// EffectOutcome is set only after desired-state evidence supports it.
type EffectOutcome string

const (
	OutcomeApplied          EffectOutcome = "applied"
	OutcomeAlreadySatisfied EffectOutcome = "already_satisfied"
)

var actionTransitions = map[ActionState]map[ActionState]struct{}{
	ActionQueued: {
		ActionRunning: {}, ActionWaitingDependency: {}, ActionNeedsReview: {},
		ActionSucceeded: {}, ActionCancelled: {}, ActionDeadlineExceeded: {},
	},
	ActionRunning: {
		ActionWaitingDependency: {}, ActionReconciling: {}, ActionNeedsReview: {},
		ActionSucceeded: {}, ActionFailed: {}, ActionCancelled: {}, ActionDeadlineExceeded: {},
	},
	ActionWaitingDependency: {
		ActionQueued: {}, ActionRunning: {}, ActionNeedsReview: {}, ActionCancelled: {}, ActionDeadlineExceeded: {},
	},
	ActionReconciling: {
		ActionQueued: {}, ActionRunning: {}, ActionSucceeded: {}, ActionNeedsReview: {},
		ActionFailed: {}, ActionCancelled: {}, ActionDeadlineExceeded: {},
	},
	ActionNeedsReview: {
		ActionQueued: {}, ActionRunning: {}, ActionCancelled: {}, ActionDeadlineExceeded: {},
	},
}

var workflowTransitions = map[WorkflowState]map[WorkflowState]struct{}{
	WorkflowAwaitingApproval: {
		WorkflowRunning: {}, WorkflowCancelled: {}, WorkflowDeadlineExceeded: {},
	},
	WorkflowRunning: {
		WorkflowWaitingDependency: {}, WorkflowNeedsReview: {}, WorkflowSucceeded: {},
		WorkflowFailed: {}, WorkflowCancelled: {}, WorkflowDeadlineExceeded: {},
	},
	WorkflowWaitingDependency: {
		WorkflowRunning: {}, WorkflowNeedsReview: {}, WorkflowCancelled: {}, WorkflowDeadlineExceeded: {},
	},
	WorkflowNeedsReview: {
		WorkflowRunning: {}, WorkflowCancelled: {}, WorkflowDeadlineExceeded: {},
	},
}

var stepTransitions = map[StepState]map[StepState]struct{}{
	StepQueued: {
		StepRunning: {}, StepBlocked: {}, StepSkipped: {}, StepCancelled: {},
	},
	StepRunning: {
		StepSucceeded: {}, StepFailed: {}, StepBlocked: {}, StepCancelled: {},
	},
	StepBlocked: {
		StepQueued: {}, StepCancelled: {},
	},
}

var attemptTransitions = map[AttemptState]map[AttemptState]struct{}{
	AttemptRunning: {
		AttemptReconciling: {}, AttemptSucceeded: {}, AttemptFailed: {}, AttemptCancelled: {},
	},
	AttemptReconciling: {
		AttemptSucceeded: {}, AttemptFailed: {}, AttemptCancelled: {},
	},
}

// CanTransition reports whether an action state change is permitted by the
// durable execution protocol.
func CanTransition(from, to ActionState) bool {
	_, ok := actionTransitions[from][to]
	return ok
}

// TransitionAction validates and returns the requested state change.
func TransitionAction(from, to ActionState) error {
	if !CanTransition(from, to) {
		return fmt.Errorf("action cannot transition from %q to %q", from, to)
	}
	return nil
}

func (state ActionState) Valid() bool {
	if _, ok := actionTransitions[state]; ok {
		return true
	}
	for _, next := range actionTransitions {
		if _, ok := next[state]; ok {
			return true
		}
	}
	return false
}

func (state ActionState) Terminal() bool {
	return state == ActionSucceeded || state == ActionFailed || state == ActionCancelled || state == ActionDeadlineExceeded
}

// CanTransitionWorkflow reports whether a workflow state change is allowed.
func CanTransitionWorkflow(from, to WorkflowState) bool {
	_, ok := workflowTransitions[from][to]
	return ok
}

func TransitionWorkflow(from, to WorkflowState) error {
	if !CanTransitionWorkflow(from, to) {
		return fmt.Errorf("workflow cannot transition from %q to %q", from, to)
	}
	return nil
}

// CanTransitionStep reports whether a workflow step state change is allowed.
func CanTransitionStep(from, to StepState) bool {
	_, ok := stepTransitions[from][to]
	return ok
}

func TransitionStep(from, to StepState) error {
	if !CanTransitionStep(from, to) {
		return fmt.Errorf("step cannot transition from %q to %q", from, to)
	}
	return nil
}

// CanTransitionAttempt reports whether an attempt state change is allowed.
func CanTransitionAttempt(from, to AttemptState) bool {
	_, ok := attemptTransitions[from][to]
	return ok
}

func TransitionAttempt(from, to AttemptState) error {
	if !CanTransitionAttempt(from, to) {
		return fmt.Errorf("attempt cannot transition from %q to %q", from, to)
	}
	return nil
}

func (state WorkflowState) Terminal() bool {
	return state == WorkflowSucceeded || state == WorkflowFailed || state == WorkflowCancelled || state == WorkflowDeadlineExceeded
}

func (state WorkflowState) Valid() bool {
	if _, ok := workflowTransitions[state]; ok {
		return true
	}
	for _, next := range workflowTransitions {
		if _, ok := next[state]; ok {
			return true
		}
	}
	return false
}

func (state StepState) Terminal() bool {
	return state == StepSucceeded || state == StepFailed || state == StepSkipped || state == StepCancelled
}

func (state StepState) Valid() bool {
	if _, ok := stepTransitions[state]; ok {
		return true
	}
	for _, next := range stepTransitions {
		if _, ok := next[state]; ok {
			return true
		}
	}
	return false
}

func (state AttemptState) Terminal() bool {
	return state == AttemptSucceeded || state == AttemptFailed || state == AttemptCancelled
}

func (state AttemptState) Valid() bool {
	if _, ok := attemptTransitions[state]; ok {
		return true
	}
	for _, next := range attemptTransitions {
		if _, ok := next[state]; ok {
			return true
		}
	}
	return false
}

// Validate reports whether kind is part of the typed action contract.
func (kind ActionKind) Valid() bool {
	switch kind {
	case ActionArrRegistration, ActionArrImport, ActionFSCopy, ActionFSHardlink,
		ActionFSMove, ActionFSRename, ActionClientStop, ActionClientRemove,
		ActionFSTrash, ActionFSRestore, ActionFSDelete, ActionDescriptorDelete,
		ActionJellyfinRefresh:
		return true
	default:
		return false
	}
}

func (outcome EffectOutcome) Valid() bool {
	return outcome == OutcomeApplied || outcome == OutcomeAlreadySatisfied
}
