package client

import (
	"reflect"
	"testing"
)

func TestMemoWithWorkflowUI(t *testing.T) {
	t.Parallel()
	memo := map[string]any{"owner": "docs"}
	got := memoWithWorkflowUI(memo, &WorkflowUIMetadata{
		DisplayName: " Example workflow ",
		Description: " Visible in the console ",
		Details: map[string]any{
			" customer ": "acme",
			"":           "ignored",
		},
		Tags: []string{" sdk ", "", "demo"},
	})

	want := map[string]any{
		"owner": "docs",
		"postgrip.ui": map[string]any{
			"displayName": "Example workflow",
			"description": "Visible in the console",
			"details": map[string]any{
				"customer": "acme",
			},
			"tags": []string{"sdk", "demo"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("memoWithWorkflowUI() = %#v, want %#v", got, want)
	}
	if _, ok := memo["postgrip.ui"]; ok {
		t.Fatalf("memoWithWorkflowUI mutated the input memo: %#v", memo)
	}
}
