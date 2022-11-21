package ghworkflow

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/go-github/v48/github"
	"github.com/h2non/gock"
	"github.com/stretchr/testify/assert"
)

const (
	expectedOwner string = "owner"
	expectedRepo  string = "repo"
	expectedWorkflowPath string = "workflow.yaml"
	expectedGitRef string = "main"
)

type dispatchBody struct{
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
	expectedCreatedFilter := fmt.Sprintf(">%s", time.Now().Add(-24 * time.Hour).Truncate(24 * time.Hour).Format("2006-01-02"))
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
			"id": expectedRunID,
		})
		gock.New("https://api.github.com").
			Get(expectedGetRunPath).
			Reply(200).
			JSON(map[string]interface{}{
				"status": "queued",
				"id": expectedRunID,
			})
			gock.New("https://api.github.com").
			Get(expectedGetRunPath).
			Reply(200).
			JSON(map[string]interface{}{
				"status": "completed",
				"id": expectedRunID,
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
	})

	t.Run("failed", func(t *testing.T) {
		gock.New("https://api.github.com").
		Get(expectedGetRunPath).
		Reply(200).
		JSON(map[string]interface{}{
			"status": "queued",
			"id": expectedRunID,
		})
		gock.New("https://api.github.com").
			Get(expectedGetRunPath).
			Reply(200).
			JSON(map[string]interface{}{
				"status": "queued",
				"id": expectedRunID,
			})
			gock.New("https://api.github.com").
			Get(expectedGetRunPath).
			Reply(200).
			JSON(map[string]interface{}{
				"status": "completed",
				"id": expectedRunID,
				"conclusion": "failure",
				"html_url": "https://gh.my/my",
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
	tests := []struct{
		name string
		eventType string
		gitRef string
		gitHeadRef string
		gitSHA string
		expectedGitRef string
		expectError bool
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
	r := &WorkflowRun{}
	expectedMaxRetyPeriod := 10 * time.Minute
	f := WithMaxRetryPeriod(expectedMaxRetyPeriod)

	err := f(r)

	assert.NoError(t, err)
	assert.Equal(t, expectedMaxRetyPeriod, r.maxRetryPeriod)
}
