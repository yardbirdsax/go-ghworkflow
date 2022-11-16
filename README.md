# go-ghworkflow

`go-ghworkflow` is a Go library that gives a high level interface for working with GitHub Actions
workflows. Currently the feature set includes a series of things for invoking GitHub Actions
workflows using the [`workflow_dispatch`
event](https://docs.github.com/en/actions/using-workflows/events-that-trigger-workflows#workflow_dispatch),
using a pipeline driven style similar to [`bitfield/script`](https://github.com/bitfield/script).

Right now, it mostly works only in the context of something run in a GitHub workflow, because:

* It sources the token used for authentication from the environment variable `GITHUB_TOKEN`
* It determines the repository where the called workflow lives using the environment variable `GITHUB_REPOSITORY`.

These things should be made more flexible as time allows to make the library more useful. PRs
welcome!

## Usage

**IMPORTANT:** For this module to work, your GitHub workflow _must_ meet the following criteria:

* It must accept an input with the name `caller_run_id` with a type of `string`.
* It must create a job whose name contains the value of that input.

This is necessary because of how the GitHub API does not return an identifier for the workflow run
triggered by a `workflow_dispatch` event, so we have to derive it by some arbitrary relationship.

> **WARNING:** Before you can start a workflow with a `workflow_dispatch` event, a version of that
> workflow _must_ exist on the default repository branch. Otherwise, the GitHub API returns a `404
> not found` status as if the workflow does not exist. This limitation is noted in the GitHub docs
> [here](https://docs.github.com/en/actions/managing-workflow-runs/manually-running-a-workflow).

Here's a simple way to use the library to call a workflow, wait for its results, and see if it
succeeded or not.

```go
import (
  "log"

  "github.com/yardbirdsax/go-ghworkflow"
)

func main {
  err := ghworkflow.Run(".github/workflows/workflow.yaml").Wait().Error
  if err != nil {
    log.Fatal(err)
  }
}
```

Here's how to pass inputs to the workflow.

```go
import (
  "log"

  "github.com/yardbirdsax/go-ghworkflow"
)

func main {
  inputs := map[string]interface{}{
    "something": "some value",
  }
  err := ghworkflow.Run(".github/workflows/workflow.yaml", WithInputs(inputs)).Wait().Error
  if err != nil {
    log.Fatal(err)
  }
}
```

By default, the module will run the workflow on the same ref that it is executed from using
conditional logic as follows:
* If the module is used in a workflow that is executing from a PR, it will use the _source_ branch
  of the pull request.
* If the module is used in a workflow that is triggered from a `push` event, it will use the same
  branch on which the commit occurred.

You can override this using the `WithGitRef` optional function. **Please note:** There is currently
no way to specify a specific commit SHA; you can only specify a ref, which means there's some chance
that the workflow being executed will come from a different commit than the one triggering the
event. (This is a very race-like condition and you probably don't have to worry about it unless
you've got a very high rate of merges / commits to your workflow files.)

```go
err := ghworkflow.Run(
  ".github/workflows/workflow.yaml",
  WithInputs(inputs),
  WithGitRef("main")
).Wait().Error
```
