package ghworkflow

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v48/github"
	"github.com/h2non/gock"
	"github.com/stretchr/testify/assert"
)

const (
	expectedOwner        string = "owner"
	expectedRepo         string = "repo"
	expectedWorkflowPath string = "workflow.yaml"
	expectedGitRef       string = "main"
)

type dispatchBody struct {
	Inputs map[string]string `json:"inputs"`
}

func (d *dispatchBody) UnmarshalRequest(r *http.Request) error {
	bodyReader, err := r.GetBody()
	if err != nil {
		return err
	}
	defer bodyReader.Close()
	body, err := io.ReadAll(bodyReader)
	if err != nil {
		return err
	}
	err = d.Unmarshal(body)
	return err
}

func (d *dispatchBody) Unmarshal(b []byte) error {
	err := json.Unmarshal(b, d)
	if err != nil {
		return err
	}
	return nil
}

func TestRun(t *testing.T) {
	expectedCallerRunID := "ASdvdf2341"
	expectedRunID := 12345678910
	otherRunID := 978351354658
	filterFunc := func(r *http.Request) bool {
		d := dispatchBody{}
		err := d.UnmarshalRequest(r)
		if err != nil {
			return false
		}
		var exists bool
		_, exists = d.Inputs["caller_run_id"]
		if !exists {
			return false
		}
		actualOtherInput, exists := d.Inputs["something_else"]
		return exists && actualOtherInput == "something"
	}
	expectedInputs := map[string]interface{}{
		"something_else": "something",
	}
	expectedCreatedFilter := fmt.Sprintf(">%s", time.Now().Add(-24*time.Hour).Truncate(24*time.Hour).Format("2006-01-02"))
	t.Setenv("GITHUB_TOKEN", "abcd123456")
	t.Setenv("GITHUB_EVENT_NAME", "push")
	t.Setenv("GITHUB_REF", expectedGitRef)
	t.Setenv("GITHUB_REPOSITORY", fmt.Sprintf("%s/%s", expectedOwner, expectedRepo))
	defer gock.Off()
	gock.New("https://api.github.com").
		Post(fmt.Sprintf("/repos/%s/%s/actions/workflows/%s/dispatches", expectedOwner, expectedRepo, expectedWorkflowPath)).
		Filter(filterFunc).
		Reply(204)
	gock.New("https://api.github.com").
		Get(fmt.Sprintf("/repos/%s/%s/actions/workflows/%s/runs", expectedOwner, expectedRepo, expectedWorkflowPath)).
		MatchParam("created", expectedCreatedFilter).
		Reply(200).
		JSON(
			map[string]interface{}{
				"total_count": 2,
				"workflow_runs": []map[string]interface{}{
					{
						"id": otherRunID,
					},
					{
						"id": expectedRunID,
					},
				},
			},
		)
	gock.New("https://api.github.com").
		Get(fmt.Sprintf("/repos/%s/%s/actions/runs/%d/jobs", expectedOwner, expectedRepo, otherRunID)).
		Reply(200).
		JSON(
			map[string]interface{}{
				"total_count": 1,
				"jobs": []map[string]interface{}{
					{
						"name": "something something",
					},
				},
			},
		)
	gock.New("https://api.github.com").
		Get(fmt.Sprintf("/repos/%s/%s/actions/runs/%d/jobs", expectedOwner, expectedRepo, expectedRunID)).
		Reply(200).
		JSON(map[string]interface{}{
			"total_count": 1,
			"jobs": []map[string]interface{}{
				{
					"name": fmt.Sprintf("something something %s", expectedCallerRunID),
				},
			},
		})

	workflowRun := NewWorkflowRun(expectedWorkflowPath, WithInputs(expectedInputs))
	workflowRun.CallerRunID = expectedCallerRunID
	workflowRun.maxRetryPeriod = 10 * time.Second
	workflowRun.Run()

	assert.False(t, gock.IsPending(), "not all expected gock calls have been made")
	assert.NoError(t, workflowRun.Error)
}

func TestWait(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "abcd123456")
	t.Setenv("GITHUB_REPOSITORY", fmt.Sprintf("%s/%s", expectedOwner, expectedRepo))
	t.Setenv("GITHUB_EVENT_NAME", "push")
	t.Setenv("GITHUB_REF", expectedGitRef)
	defer gock.Off()
	var expectedRunID int64 = 12345678
	expectedGetRunPath := fmt.Sprintf("/repos/%s/%s/actions/runs/%d", expectedOwner, expectedRepo, expectedRunID)

	t.Run("suceeded", func(t *testing.T) {
		gock.New("https://api.github.com").
			Get(expectedGetRunPath).
			Reply(200).
			JSON(map[string]interface{}{
				"status": "queued",
				"id":     expectedRunID,
			})
		gock.New("https://api.github.com").
			Get(expectedGetRunPath).
			Reply(200).
			JSON(map[string]interface{}{
				"status": "queued",
				"id":     expectedRunID,
			})
		gock.New("https://api.github.com").
			Get(expectedGetRunPath).
			Reply(200).
			JSON(map[string]interface{}{
				"status":     "completed",
				"id":         expectedRunID,
				"conclusion": "success",
			})

		workflowRun := NewWorkflowRun(expectedWorkflowPath)
		workflowRun.WorkflowRun = &github.WorkflowRun{
			ID: &expectedRunID,
		}
		workflowRun.maxRetryPeriod = 10 * time.Second
		workflowRun.Wait()

		assert.NoError(t, workflowRun.Error)
		assert.Equal(t, "completed", *workflowRun.WorkflowRun.Status)
		assert.Equal(t, "success", *workflowRun.WorkflowRun.Conclusion)
		assert.False(t, gock.IsPending(), "gock has pending requests")
	})

	t.Run("failed", func(t *testing.T) {
		gock.New("https://api.github.com").
			Get(expectedGetRunPath).
			Reply(200).
			JSON(map[string]interface{}{
				"status": "queued",
				"id":     expectedRunID,
			})
		gock.New("https://api.github.com").
			Get(expectedGetRunPath).
			Reply(200).
			JSON(map[string]interface{}{
				"status": "queued",
				"id":     expectedRunID,
			})
		gock.New("https://api.github.com").
			Get(expectedGetRunPath).
			Reply(200).
			JSON(map[string]interface{}{
				"status":     "completed",
				"id":         expectedRunID,
				"conclusion": "failure",
				"html_url":   "https://gh.my/my",
			})

		workflowRun := NewWorkflowRun(expectedWorkflowPath)
		workflowRun.WorkflowRun = &github.WorkflowRun{
			ID: &expectedRunID,
		}
		workflowRun.maxRetryPeriod = 10 * time.Second
		workflowRun.Wait()

		assert.EqualError(t, workflowRun.Error, "workflow execution failed, see details at 'https://gh.my/my'")
		assert.Equal(t, "completed", *workflowRun.WorkflowRun.Status)
		assert.Equal(t, "failure", *workflowRun.WorkflowRun.Conclusion)
		assert.False(t, gock.IsPending(), "gock has pending requests")
	})

	t.Run("waiting", func(t *testing.T) {
		htmlURL := fmt.Sprintf("https://github.com/%s/%s/actions/runs/%d", expectedOwner, expectedRepo, expectedRunID)
		logChan := make(chan (logMessage))
		gock.New("https://api.github.com").
			Get(expectedGetRunPath).
			Reply(200).
			JSON(map[string]interface{}{
				"status": "queued",
				"id":     expectedRunID,
			})
		gock.New("https://api.github.com").
			Get(expectedGetRunPath).
			Reply(200).
			JSON(map[string]interface{}{
				"status":   "waiting",
				"id":       expectedRunID,
				"html_url": htmlURL,
			})
		gock.New("https://api.github.com").
			Get(expectedGetRunPath).
			Reply(200).
			JSON(map[string]interface{}{
				"status":   "waiting",
				"id":       expectedRunID,
				"html_url": htmlURL,
			})
		gock.New("https://api.github.com").
			Get(expectedGetRunPath).
			Reply(200).
			JSON(map[string]interface{}{
				"status":     "completed",
				"id":         expectedRunID,
				"conclusion": "success",
			})

		workflowRun := NewWorkflowRun(expectedWorkflowPath, WithLoggingChannel(logChan))
		workflowRun.WorkflowRun = &github.WorkflowRun{
			ID: &expectedRunID,
		}
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			numLogMessages := 0
		outerloop:
			for {
				select {
				case <-logChan:
					numLogMessages++
				case <-time.After(5 * time.Second):
					break outerloop
				}
			}
			assert.Equal(t, 2, numLogMessages, "did not receive the expected number of log messages")
			wg.Done()
		}()
		workflowRun.Wait()
		wg.Wait()

		assert.NoError(t, workflowRun.Error)
		assert.Equal(t, "completed", *workflowRun.WorkflowRun.Status)
		assert.Equal(t, "success", *workflowRun.WorkflowRun.Conclusion)
		assert.False(t, gock.IsPending(), "gock has pending requests")
	})
}

func TestWithGitRef(t *testing.T) {
	expectedGitRef := "refs/something/head"
	f := WithGitRef(expectedGitRef)
	r := &WorkflowRun{}

	err := f(r)
	assert.NoError(t, err)
	assert.Equal(t, expectedGitRef, r.ref)
}

func TestGetGitRef(t *testing.T) {
	tests := []struct {
		name           string
		eventType      string
		gitRef         string
		gitHeadRef     string
		gitSHA         string
		expectedGitRef string
		expectError    bool
	}{
		{
			"push",
			"push",
			"main",
			"",
			"asdasd2334r",
			"main",
			false,
		},
		{
			"pull_request",
			"pull_request",
			"refs/pull/1/merge",
			"feature-1",
			"asdasd2334r",
			"feature-1",
			false,
		},
		{
			"repository_dispatch",
			"repository_dispatch",
			"refs/pull/1/merge",
			"feature-1",
			"asdasd2334r",
			"feature-1",
			true,
		},
	}

	for _, tc := range tests {
		// This is necessary for parallel subtests
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GITHUB_EVENT_NAME", tc.eventType)
			t.Setenv("GITHUB_REF", tc.gitRef)
			t.Setenv("GITHUB_HEAD_REF", tc.gitHeadRef)
			t.Setenv("GITHUB_SHA", tc.gitSHA)
			r := &WorkflowRun{}

			r.getGitRef()

			if !tc.expectError {
				assert.NoError(t, r.Error)
				assert.Equal(t, tc.expectedGitRef, r.ref)
			} else {
				assert.Error(t, r.Error)
			}
		})
	}
}

func TestWithTimeout(t *testing.T) {
	r := NewWorkflowRun("path")
	expectedMaxRetyPeriod := 10 * time.Minute
	f := WithMaxRetryPeriod(expectedMaxRetyPeriod)

	err := f(r)

	assert.NoError(t, err)
	assert.Equal(t, expectedMaxRetyPeriod, r.maxRetryPeriod)
}

func TestWithRepo(t *testing.T) {
	testCases := []struct {
		name           string
		owner          string
		repo           string
		input          *WorkflowRun
		expectedResult *WorkflowRun
		expectedError  error
	}{
		{
			name:  "both values specified",
			owner: "owner",
			repo:  "repo",
			expectedResult: &WorkflowRun{
				owner: "owner",
				repo:  "repo",
			},
			expectedError: nil,
		},
		{
			name:  "only repo specified",
			owner: "",
			repo:  "repo",
			expectedResult: &WorkflowRun{
				owner: "owner",
				repo:  "repo",
			},
			input: &WorkflowRun{
				owner: "owner",
				repo:  "repo2",
			},
			expectedError: nil,
		},
		{
			name:  "repo not specified",
			owner: "owner",
			repo:  "",
			expectedResult: &WorkflowRun{
				owner: "owner",
				repo:  "repo2",
			},
			input: &WorkflowRun{
				owner: "owner",
				repo:  "repo2",
			},
			expectedError: fmt.Errorf("you must specify a repository name at minimum"),
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var actualResult *WorkflowRun
			if tc.input == nil {
				actualResult = &WorkflowRun{}
			} else {
				actualResult = tc.input
			}
			f := WithRepo(tc.owner, tc.repo)

			err := f(actualResult)

			assert.EqualValues(t, tc.expectedResult, actualResult)
			if tc.expectedError == nil {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedError.Error())
			}
		})
	}
}

func TestWithDeadline(t *testing.T) {
	deadline := time.Now().Add(10 * time.Second)
	r := NewWorkflowRun("path", WithDeadline(deadline))
	actualDeadline, ok := r.ctx.Deadline()
	assert.True(t, ok, "deadline was not set")
	assert.Equal(t, deadline, actualDeadline, "deadline was not expected value")
}

func TestLog(t *testing.T) {
	logChan := make(chan (logMessage))
	r := NewWorkflowRun("path", WithLoggingChannel(logChan), WithDeadline(time.Now().Add(30*time.Second)))
	expectedLogMessage := "a log message"
	expectedLogLevel := Warning
	go func() {
		r.log(expectedLogMessage, expectedLogLevel)
	}()

	select {
	case <-r.ctx.Done():
		t.Error("timeout exceeded without receiving message")
	case actualLogMessage := <-logChan:
		assert.Equal(t, expectedLogMessage, actualLogMessage.Message)
		assert.Equal(t, expectedLogLevel, actualLogMessage.Level)
	}
}
