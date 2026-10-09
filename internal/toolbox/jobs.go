package toolbox

import (
	"context"
	"fmt"
	"regexp"
	"sync"
)

// Paid generation runs at the provider after Facet submits it, and the call
// that submitted it can be cut off while it waits: by a harness time limit, a
// cancellation, or a lost connection. The provider job keeps running and is
// usually charged already. Facet therefore reports a job's id as soon as the
// provider assigns it, in a progress milestone, in the error details of a call
// that ends without the media, and in the result and the artifact
// provenance. A tool that submits provider jobs accepts resume_job_id, which
// collects that job instead of submitting, and paying for, another one.

// Progress is a milestone a running tool reports before its result.
type Progress struct {
	Tool    string `json:"tool"`
	Message string `json:"message"`
	// ProviderJobID is set when the milestone is a provider job the call
	// submitted or resumed.
	ProviderJobID string `json:"provider_job_id,omitempty"`
}

type progressKey struct{}

// WithProgress returns a context whose tool calls report milestones to
// report. Tools call it synchronously, so it must return promptly.
func WithProgress(ctx context.Context, report func(Progress)) context.Context {
	if report == nil {
		return ctx
	}
	return context.WithValue(ctx, progressKey{}, report)
}

type providerJobKey struct{}

// providerJob holds the provider job one run is waiting for.
type providerJob struct {
	mu sync.Mutex
	id string
}

// withProviderJob returns a context in which a tool's provider job is
// recorded, and the record.
func withProviderJob(ctx context.Context) (context.Context, *providerJob) {
	job := &providerJob{}
	return context.WithValue(ctx, providerJobKey{}, job), job
}

func (j *providerJob) ID() string {
	if j == nil {
		return ""
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.id
}

// noteProviderJob records the provider job a run is waiting for and reports
// it at once, so the id is known even if the run never returns.
func noteProviderJob(ctx context.Context, tool, id string, resumed bool) {
	if ctx == nil || id == "" {
		return
	}
	if job, ok := ctx.Value(providerJobKey{}).(*providerJob); ok {
		job.mu.Lock()
		job.id = id
		job.mu.Unlock()
	}
	report, ok := ctx.Value(progressKey{}).(func(Progress))
	if !ok {
		return
	}
	message := fmt.Sprintf("%s submitted provider job %s; if this call is cut off, run the same request "+
		"with \"resume_job_id\": %q to collect the job instead of submitting a new one", tool, id, id)
	if resumed {
		message = fmt.Sprintf("%s is collecting provider job %s; no new job was submitted", tool, id)
	}
	report(Progress{Tool: tool, Message: message, ProviderJobID: id})
}

// providerJobIDPattern is the shape of a provider job id. The id becomes part
// of a provider URL, so only a plain identifier is accepted.
var providerJobIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// resumeJobIDSchema is the request property of every tool that accepts
// resume_job_id.
var resumeJobIDSchema = map[string]any{
	"type": "string", "pattern": providerJobIDPattern.String(),
	"description": "Collect the provider job a previous call of this tool reported (provider_job_id) " +
		"instead of submitting a new one. Send the same request with this field added.",
}

// validResumeJobID checks a request's resume_job_id; "" means none.
func validResumeJobID(id string) error {
	if id == "" || providerJobIDPattern.MatchString(id) {
		return nil
	}
	return failure("invalid_request", "resume_job_id must be the provider job id a previous call reported "+
		"(letters, digits, '.', '_' and '-', at most 128 characters)", nil)
}

// withProviderJobError adds a recorded provider job to the error envelope of
// a run that ended without its media. Unless the tool reported the job as
// over (details.provider_job_status), sending the same request again would
// submit, and pay for, a second job, so the error is not retryable as it
// stands, and the message says how to collect the job instead.
func withProviderJobError(env Envelope, id string) Envelope {
	if id == "" || env.Error == nil {
		return env
	}
	if env.Error.Details == nil {
		env.Error.Details = map[string]any{}
	}
	env.Error.Details["provider_job_id"] = id
	if _, over := env.Error.Details["provider_job_status"]; over {
		return env
	}
	env.Error.Retryable = false
	env.Error.Message = bounded(env.Error.Message) + fmt.Sprintf("; provider job %s may still be running and "+
		"may already be charged: run the same request with \"resume_job_id\": %q to collect it without a new submission", id, id)
	return env
}
