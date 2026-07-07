//revive:disable:exported package-comments
package ai

import (
	"context"
	"strings"
)

func FailureRequest(podSpec, podStatus, events string) Request {
	return Request{
		System: "You explain Kubernetes pod failures with concise likely causes and fixes.",
		Prompt: strings.Join([]string{"Pod spec:", podSpec, "Pod status:", podStatus, "Events:", events}, "\n"),
	}
}

func LogSummaryRequest(logs string) Request {
	return Request{System: "Summarize Kubernetes logs and identify likely issues.", Prompt: logs}
}

func LogQuestionRequest(logs, question string) Request {
	return Request{
		System: "Answer follow-up questions about Kubernetes logs using only the provided log context.",
		Prompt: strings.Join([]string{"Log context:", logs, "Question:", question}, "\n"),
	}
}

func ManifestRequest(manifest string) Request {
	return Request{System: "Explain and lint this Kubernetes manifest.", Prompt: manifest}
}

// ChatRequest is a general cluster-aware chat turn with injected context.
func ChatRequest(clusterContext, question string) Request {
	return Request{
		System: "You are a Kubernetes assistant embedded in a terminal UI. Use the provided cluster context when relevant and give concise, actionable answers.",
		Prompt: strings.Join([]string{"Cluster context:", clusterContext, "Question:", question}, "\n"),
	}
}

func NaturalLanguageAction(ctx context.Context, provider LLMProvider, actionCatalog, query string) (Response, error) {
	return provider.Complete(ctx, Request{System: "Return a structured Kubernetes TUI action or a clarifying question.", Prompt: actionCatalog + "\n\nUser intent: " + query})
}
