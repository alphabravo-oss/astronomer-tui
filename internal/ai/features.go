//revive:disable:exported package-comments
package ai

import (
	"context"
	"strings"
)

func ExplainFailure(ctx context.Context, provider LLMProvider, podSpec, podStatus, events string) (Response, error) {
	return provider.Complete(ctx, FailureRequest(podSpec, podStatus, events))
}

func StreamFailure(ctx context.Context, provider LLMProvider, podSpec, podStatus, events string) (<-chan Chunk, error) {
	return provider.Stream(ctx, FailureRequest(podSpec, podStatus, events))
}

func FailureRequest(podSpec, podStatus, events string) Request {
	return Request{
		System: "You explain Kubernetes pod failures with concise likely causes and fixes.",
		Prompt: strings.Join([]string{"Pod spec:", podSpec, "Pod status:", podStatus, "Events:", events}, "\n"),
	}
}

func SummarizeLogs(ctx context.Context, provider LLMProvider, logs string) (Response, error) {
	return provider.Complete(ctx, LogSummaryRequest(logs))
}

func StreamLogSummary(ctx context.Context, provider LLMProvider, logs string) (<-chan Chunk, error) {
	return provider.Stream(ctx, LogSummaryRequest(logs))
}

func LogSummaryRequest(logs string) Request {
	return Request{System: "Summarize Kubernetes logs and identify likely issues.", Prompt: logs}
}

func AskLogs(ctx context.Context, provider LLMProvider, logs, question string) (Response, error) {
	return provider.Complete(ctx, LogQuestionRequest(logs, question))
}

func StreamLogQuestion(ctx context.Context, provider LLMProvider, logs, question string) (<-chan Chunk, error) {
	return provider.Stream(ctx, LogQuestionRequest(logs, question))
}

func LogQuestionRequest(logs, question string) Request {
	return Request{
		System: "Answer follow-up questions about Kubernetes logs using only the provided log context.",
		Prompt: strings.Join([]string{"Log context:", logs, "Question:", question}, "\n"),
	}
}

func ExplainManifest(ctx context.Context, provider LLMProvider, manifest string) (Response, error) {
	return provider.Complete(ctx, ManifestRequest(manifest))
}

func StreamManifest(ctx context.Context, provider LLMProvider, manifest string) (<-chan Chunk, error) {
	return provider.Stream(ctx, ManifestRequest(manifest))
}

func ManifestRequest(manifest string) Request {
	return Request{System: "Explain and lint this Kubernetes manifest.", Prompt: manifest}
}

func NaturalLanguageAction(ctx context.Context, provider LLMProvider, actionCatalog, query string) (Response, error) {
	return provider.Complete(ctx, Request{System: "Return a structured Kubernetes TUI action or a clarifying question.", Prompt: actionCatalog + "\n\nUser intent: " + query})
}
