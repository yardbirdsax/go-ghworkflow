package ghworkflow

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/go-github/v48/github"
	"golang.org/x/oauth2"
)

// Run executes a workflow by path and returns a WorkflowRun struct for the
// current execution. To use this properly, the workflow _must_ accept an input
// called 'caller_run_id', and have a job that will use that input in its name.
func Run(path string, optsFn ...workflowOptsFn) *WorkflowRun {
	workflowRun := NewWorkflowRun(path, optsFn...)

	return workflowRun.Run()
}

func getGitHubClient(ctx context.Context) (*github.Client, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("could not acquire GitHub token from environment variable GITHUB_TOKEN")
	}
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)
	return client, nil
}

type workflowOptsFn func(*WorkflowRun) error

// WithInputs sets the inputs for the workflow dispatch.
func WithInputs(inputs map[string]interface{}) workflowOptsFn {
	return func(r *WorkflowRun) error {
		r.inputs = inputs
		return nil
	}
}

// WithGitRef sets the Git ref used when creating the dispatch event.
// This is the ref that the Actions platform will use for sourcing the
// workflow code.
func WithGitRef(ref string) workflowOptsFn {
	return func(r *WorkflowRun) error {
		r.ref = ref
		return nil
	}
}

// WithMaxRetryPeriod sets the maximum time to wait for the workflow run to start and complete.
func WithMaxRetryPeriod(period time.Duration) workflowOptsFn {
	return func(r *WorkflowRun) error {
		r.maxRetryPeriod = period
		return nil
	}
}

// WithDeadline sets the deadline for the workflow run to start and complete.
func WithDeadline(deadline time.Time) workflowOptsFn {
	return func(r *WorkflowRun) error {
		r.ctx, r.cancelFunc = context.WithDeadline(r.ctx, deadline)
		return nil
	}
}

// WithRepo lets you specify a different repository where the workflow should be invoked. You
// must always specify the repository name; if the owner name is a blank string, the current
// value will not be replaced.
func WithRepo(owner string, repo string) workflowOptsFn {
	return func(r *WorkflowRun) error {
		if strings.TrimSpace(repo) == "" {
			return fmt.Errorf("you must specify a repository name at minimum")
		}
		if strings.TrimSpace(owner) != "" {
			r.owner = owner
		}
		r.repo = repo
		return nil
	}
}

// LogLevel defines the severity or level of a log message sent by the library
// to the configured logging channel.
type logLevel int

const (
	Error logLevel = iota
	Warning
	Info
	Debug
	Trace
)

func (l logLevel) String() string {
	switch l {
	case Error:
		return "error"
	case Warning:
		return "warning"
	case Info:
		return "info"
	case Debug:
		return "debug"
	case Trace:
		return "trace"
	default:
		return "unknown"
	}
}

type logMessage struct {
	Level logLevel
	Message string
}

// WithLoggingChannel allows you to pass in a Go channel that will receive events from
// long running operations. Events are sent asynchronously and will not block execution.
func WithLoggingChannel(logChan chan(logMessage)) workflowOptsFn {
	return func(wr *WorkflowRun) error {
		wr.logChannel = logChan
		return nil
	}
}

// WorkflowRun is a struct representing a single run of a workflow.
type WorkflowRun struct {
	// Owner of the repository
	owner string
	// Name of the repository
	repo string
	// The Git ref to call the workflow on
	ref string
	// The inputs passed through the dispatch event
	inputs map[string]interface{}
	// Reference to the workflow definition
	Workflow *github.Workflow
	// Reference to the workflow run
	WorkflowRun *github.WorkflowRun
	// An identifier that is used to tie the workflow dispatch event to the workflow run.
	CallerRunID string
	// The error that caused the workflow to either fail to start or fail during execution.
	Error error
	// The context for the run.
	ctx context.Context
	// The cancel function for the context
	cancelFunc context.CancelFunc
	// The maximum retry period for long running operations
	maxRetryPeriod time.Duration
	// A channel for sending log messages to
	logChannel chan(logMessage)
}

func NewWorkflowRun(path string, optsFn ...workflowOptsFn) *WorkflowRun {
	rand.Seed(time.Now().UnixNano())
	workflowRun := &WorkflowRun{
		ctx: context.TODO(),
		Workflow: &github.Workflow{
			Path: &path,
		},
		maxRetryPeriod: 5 * time.Minute,
		CallerRunID:    fmt.Sprint(rand.Int()),
	}

	workflowRun.getGitHubRepository()
	workflowRun.getGitRef()

	for _, f := range optsFn {
		err := f(workflowRun)
		if err != nil {
			workflowRun.Error = err
			return workflowRun
		}
	}

	return workflowRun
}

func (r *WorkflowRun) getGitHubRepository() {
	if r.Error != nil {
		return
	}
	githubRepositoryEnvValue := os.Getenv("GITHUB_REPOSITORY")
	owner := ""
	repo := ""
	if githubRepositoryEnvValue != "" {
		splitRepositoryValue := strings.Split(githubRepositoryEnvValue, "/")
		owner = splitRepositoryValue[0]
		if len(splitRepositoryValue) >= 2 {
			repo = splitRepositoryValue[1]
		}
	}
	r.owner = owner
	r.repo = repo
}

func (r *WorkflowRun) getGitRef() {
	if r.Error != nil {
		return
	}
	if r.ref == "" {
		eventTypeName := os.Getenv("GITHUB_EVENT_NAME")
		switch eventTypeName {
		case "pull_request":
			r.ref = os.Getenv("GITHUB_HEAD_REF")
		case "push":
			r.ref = os.Getenv("GITHUB_REF")
		default:
			r.Error = fmt.Errorf("unsupported event type")
		}
		if r.ref == "" {
			r.Error = fmt.Errorf("unable to determine Git SHA value to call workflow")
		}
	}
}

func (r *WorkflowRun) getWorkflowRun() {
	if r.Error != nil {
		return
	}

	client, err := getGitHubClient(r.ctx)
	if err != nil {
		r.Error = err
		return
	}

	operation := func() error {
		// We only look at the last 24 hours, because there's no way our dispatch
		// kicked off a workflow in the past.
		listStartDate := time.Now().Add(-24 * time.Hour).Truncate(24 * time.Hour).Format("2006-01-02")
		opts := &github.ListWorkflowRunsOptions{
			Created: fmt.Sprintf(">%s", listStartDate),
		}
		workflowRuns, _, err := client.Actions.ListWorkflowRunsByFileName(r.ctx, r.owner, r.repo, *r.Workflow.Path, opts)
		if err != nil {
			return err
		}

	runLoop:
		for _, run := range workflowRuns.WorkflowRuns {
			jobs, _, err := client.Actions.ListWorkflowJobs(r.ctx, r.owner, r.repo, *run.ID, nil)
			if err != nil {
				return err
			}
			for _, job := range jobs.Jobs {
				if strings.Contains(*job.Name, r.CallerRunID) {
					r.WorkflowRun = run
					break runLoop
				}
			}
		}

		if r.WorkflowRun == nil {
			return fmt.Errorf("no matching workflow run found")
		}
		return nil
	}

	backOff := backoff.NewExponentialBackOff()
	backOffWithContext := backoff.WithContext(backOff, r.ctx)

	err = backoff.Retry(operation, backOffWithContext)
	if err != nil {
		r.Error = err
	}
}

// Run executes a workflow with the configured properties, then finds the
// linked workflow run by matching the struct's `CallerRunID` property with
// job names. It then fills in the workflow run details in the struct for later
// reference.
func (r *WorkflowRun) Run() *WorkflowRun {
	if r.owner == "" || r.repo == "" {
		r.getGitHubRepository()
	}
	r.getGitRef()

	if r.Error != nil {
		return r
	}
	client, err := getGitHubClient(r.ctx)
	if err != nil {
		r.Error = err
		return r
	}
	finalInputs := r.inputs
	finalInputs["caller_run_id"] = r.CallerRunID
	workflowDispatchEvent := github.CreateWorkflowDispatchEventRequest{
		Ref:    r.ref,
		Inputs: r.inputs,
	}
	_, err = client.Actions.CreateWorkflowDispatchEventByFileName(r.ctx, r.owner, r.repo, *r.Workflow.Path, workflowDispatchEvent)
	if err != nil {
		r.Error = err
		return r
	}
	r.getWorkflowRun()

	return r
}

// Wait pauses execution until the workflow completes, then populates the
// Error property of the struct if the workflow run failed.
func (r *WorkflowRun) Wait() *WorkflowRun {
	if r.Error != nil {
		return r
	}

	client, err := getGitHubClient(r.ctx)
	if err != nil {
		r.Error = err
		return r
	}

	operation := func() error {
		workflowRun, _, err := client.Actions.GetWorkflowRunByID(r.ctx, r.owner, r.repo, *r.WorkflowRun.ID)
		if err != nil {
			return err
		}
		r.WorkflowRun = workflowRun
		if *workflowRun.Status != "completed" {
			return fmt.Errorf("workflow is not yet in a completed status")
		}
		if *workflowRun.Conclusion == "failure" {
			return backoff.Permanent(fmt.Errorf("workflow execution failed, see details at '%s'", workflowRun.GetHTMLURL()))
		}
		return nil
	}

	backOff := backoff.NewExponentialBackOff()
	backOff.MaxElapsedTime = r.maxRetryPeriod
	backOffWithContext := backoff.WithContext(backOff, r.ctx)

	err = backoff.Retry(operation, backOffWithContext)
	if err != nil {
		r.Error = err
	}

	return r
}

func (r *WorkflowRun) log(message string, level logLevel) {
	if r.logChannel == nil {
		return
	}
	msg := logMessage{
		Level: level,
		Message: message,
	}
	go func() {
		select {
		case <-r.ctx.Done():
		case r.logChannel <- msg:
		}
	}()
}
