//go:build integration
package ghworkflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yardbirdsax/go-ghworkflow"
)

func TestRun_Integration(t *testing.T) {
	exampleWorkflowPath := "example.yaml"

	t.Run("success", func(t *testing.T) {
		err := ghworkflow.Run(exampleWorkflowPath, ghworkflow.WithInputs(
			map[string]interface{}{
				"fail": "false",
			},
		)).Wait().Error
		assert.NoError(t, err)
	})

	t.Run("failure", func(t *testing.T) {
		err := ghworkflow.Run(exampleWorkflowPath, ghworkflow.WithInputs(
			map[string]interface{}{
				"fail": "true",
			},
		)).Wait().Error
		assert.Error(t, err)
	})
}
