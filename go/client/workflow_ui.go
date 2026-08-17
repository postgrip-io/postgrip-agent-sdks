package client

import "strings"

const postGripUIMemoKey = "postgrip.ui"

func memoWithWorkflowUI(memo map[string]any, ui *WorkflowUIMetadata) map[string]any {
	if ui == nil {
		return memo
	}
	uiMemo := workflowUIMemo(ui)
	if len(uiMemo) == 0 {
		return memo
	}
	out := make(map[string]any, len(memo)+1)
	for key, value := range memo {
		out[key] = value
	}
	out[postGripUIMemoKey] = uiMemo
	return out
}

func workflowUIMemo(ui *WorkflowUIMetadata) map[string]any {
	out := map[string]any{}
	if displayName := strings.TrimSpace(ui.DisplayName); displayName != "" {
		out["displayName"] = displayName
	}
	if description := strings.TrimSpace(ui.Description); description != "" {
		out["description"] = description
	}
	if len(ui.Details) > 0 {
		details := make(map[string]any, len(ui.Details))
		for key, value := range ui.Details {
			if trimmed := strings.TrimSpace(key); trimmed != "" {
				details[trimmed] = value
			}
		}
		if len(details) > 0 {
			out["details"] = details
		}
	}
	if len(ui.Tags) > 0 {
		tags := make([]string, 0, len(ui.Tags))
		for _, tag := range ui.Tags {
			if trimmed := strings.TrimSpace(tag); trimmed != "" {
				tags = append(tags, trimmed)
			}
		}
		if len(tags) > 0 {
			out["tags"] = tags
		}
	}
	return out
}
