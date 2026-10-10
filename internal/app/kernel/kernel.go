// Package kernel is the Facet App's one interface to its agent engine
// (design rule R5). The window starts and stops the kernel, sends the
// person's messages, cancels a turn and answers approval requests; the kernel
// streams events back, including the approvals it needs and the turns it ended
// on its own. Nothing else in the App talks to the engine, so replacing it
// means replacing one implementation of Kernel.
package kernel

import (
	"context"
	"errors"
	"time"
)

// Kernel is an agent engine the App drives.
type Kernel interface {
	// Start brings the engine up and returns when it can take messages.
	Start(ctx context.Context) error
	// Stop shuts the engine down. Pending approvals are denied.
	Stop(ctx context.Context) error
	// Send starts a turn in session, or steers the turn running there.
	Send(ctx context.Context, session, text string) error
	// Cancel stops the turn running in session, and the tools it runs.
	Cancel(ctx context.Context, session string) error
	// Answer resolves a pending approval request.
	Answer(id string, approve bool, reason string) error
	// Events delivers everything the engine reports, in order. The channel
	// is closed after Stop.
	Events() <-chan Event
}

// EventKind names what an event reports.
type EventKind string

const (
	// EventStatus reports the engine's state in Status: starting, ready,
	// stopped or failed (Detail says why).
	EventStatus EventKind = "status"
	// EventTurnStart and EventTurnEnd bracket a turn. Status on the end is
	// completed, error or aborted, so a turn the engine ended itself reaches
	// the window as well as one the window cancelled.
	EventTurnStart EventKind = "turn_start"
	EventTurnEnd   EventKind = "turn_end"
	// EventMessage is assistant text. Text is the whole text so far, not a
	// delta; Final marks the last update of a message.
	EventMessage EventKind = "message"
	// EventMessageDeleted withdraws a message, by MessageID.
	EventMessageDeleted EventKind = "message_deleted"
	// EventToolCall is a tool the agent called, with its arguments.
	EventToolCall EventKind = "tool_call"
	// EventProgress reports a running tool's progress (seconds so far for
	// Facet's tools) with a message in Text.
	EventProgress EventKind = "progress"
	// EventApprovalRequest asks the window to answer Approval; nothing runs
	// until it does. EventApprovalResolved reports the outcome.
	EventApprovalRequest  EventKind = "approval_request"
	EventApprovalResolved EventKind = "approval_resolved"
	// EventNotice is something the engine said outside a message, such as a
	// protocol error.
	EventNotice EventKind = "notice"
)

// Event is one thing the engine reported.
type Event struct {
	Kind      EventKind      `json:"kind"`
	At        time.Time      `json:"at"`
	Session   string         `json:"session,omitempty"`
	MessageID string         `json:"message_id,omitempty"`
	Text      string         `json:"text,omitempty"`
	Final     bool           `json:"final,omitempty"`
	Thought   bool           `json:"thought,omitempty"`
	Tool      *Tool          `json:"tool,omitempty"`
	Args      map[string]any `json:"args,omitempty"`
	Progress  float64        `json:"progress,omitempty"`
	Approval  *Approval      `json:"approval,omitempty"`
	Status    string         `json:"status,omitempty"`
	Detail    string         `json:"detail,omitempty"`
}

// Tool names a tool as the engine calls it, and as Facet knows it when it is
// one of the Toolkit's.
type Tool struct {
	Name  string `json:"name"`
	Facet string `json:"facet,omitempty"`
	// Paid is set for a Facet tool whose effects may charge.
	Paid bool `json:"paid,omitempty"`
}

// Approval is a request to run a tool, and its answer once given.
type Approval struct {
	ID       string         `json:"id"`
	Tool     Tool           `json:"tool"`
	Args     map[string]any `json:"args,omitempty"`
	Origin   string         `json:"origin,omitempty"`
	Source   string         `json:"source,omitempty"`
	Deadline time.Time      `json:"deadline"`
	// Approved and Reason are set on EventApprovalResolved.
	Approved bool   `json:"approved"`
	Reason   string `json:"reason,omitempty"`
}

// Errors an implementation returns.
var (
	ErrNotRunning      = errors.New("the agent engine is not running")
	ErrUnknownApproval = errors.New("no approval request with that id is waiting")
)
