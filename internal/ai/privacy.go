package ai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sigs.k8s.io/yaml"
)

const MaxRequestBytes = 64 * 1024

var sensitiveKey = regexp.MustCompile(`(?i)(password|passwd|token|secret|credential|authorization|api[-_]?key|private[-_]?key)`)
var credentialText = regexp.MustCompile(`(?im)((?:password|passwd|token|secret|credential|authorization|api[-_]?key|value)\s*[=:]\s*)(?:"[^"\n]*"|'[^'\n]*'|[^\s,}\n]+)`)
var bearerText = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/=-]+`)
var privateKeyText = regexp.MustCompile(`(?s)-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----`)
var secretManifest = regexp.MustCompile(`(?im)(?:^\s*kind:\s*Secret\s*$|"kind"\s*:\s*"Secret")`)

// Prepare creates the exact, bounded request shown in the outgoing-data preview.
// Redaction is conservative; the preview remains necessary for arbitrary logs.
func Prepare(req Request) (Request, error) {
	data, _ := json.Marshal(req)
	if len(data) > 4*1024*1024 {
		return Request{}, fmt.Errorf("AI input exceeds 4 MiB; narrow the selection")
	}
	clean := func(text string) (string, error) {
		if secretManifest.MatchString(text) {
			return "", fmt.Errorf("Secret manifests cannot be sent to AI")
		}
		var obj map[string]any
		if yaml.Unmarshal([]byte(text), &obj) == nil && obj != nil {
			scrub(obj)
			b, err := yaml.Marshal(obj)
			if err == nil {
				text = string(b)
			}
		}
		text = privateKeyText.ReplaceAllString(text, "[REDACTED PRIVATE KEY]")
		text = bearerText.ReplaceAllString(text, "Bearer [REDACTED]")
		return credentialText.ReplaceAllString(text, "${1}[REDACTED]"), nil
	}
	out := Request{System: req.System, Context: map[string]string{}}
	var err error
	out.Prompt, err = clean(req.Prompt)
	if err != nil {
		return Request{}, err
	}
	for k, v := range req.Context {
		if sensitiveKey.MatchString(k) {
			out.Context[k] = "[REDACTED]"
			continue
		}
		out.Context[k], err = clean(v)
		if err != nil {
			return Request{}, err
		}
	}
	data, _ = json.Marshal(out)
	if len(data) > MaxRequestBytes {
		return Request{}, fmt.Errorf("AI request exceeds %d KiB after redaction; filter logs or select a smaller manifest", MaxRequestBytes/1024)
	}
	return out, nil
}
func scrub(value any) {
	switch obj := value.(type) {
	case map[string]any:
		for key, v := range obj {
			if sensitiveKey.MatchString(key) || key == "value" || key == "managedFields" || key == "annotations" {
				obj[key] = "[REDACTED]"
			} else {
				scrub(v)
			}
		}
	case []any:
		for _, v := range obj {
			scrub(v)
		}
	}
}
