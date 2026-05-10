//revive:disable:exported package-comments
package safety

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/alphabravo-oss/astrotui/internal/config"
)

type Verb string

const (
	Delete  Verb = "delete"
	Scale   Verb = "scale"
	Restart Verb = "restart"
	Exec    Verb = "exec-into-prod-pod"
	Edit    Verb = "edit"
	Patch   Verb = "patch"
)

var ErrReadOnly = errors.New("context is read-only")

func PolicyFor(cfg config.Config, currentContext string) string {
	if ctx, ok := cfg.Contexts[currentContext]; ok && ctx.Policy != "" {
		return ctx.Policy
	}
	pattern := cfg.Safety.ProdPattern
	if pattern == "" {
		pattern = `(?i)prod|production`
	}
	if regexp.MustCompile(pattern).MatchString(currentContext) {
		return "confirm-destructive"
	}
	return "full"
}

func RequiresConfirmation(cfg config.Config, currentContext string, verb Verb) bool {
	if !destructive(verb) {
		return false
	}
	return PolicyFor(cfg, currentContext) == "confirm-destructive"
}

func Authorize(cfg config.Config, currentContext string, verb Verb, typed string) error {
	policy := PolicyFor(cfg, currentContext)
	if policy == "read-only" && destructive(verb) {
		return ErrReadOnly
	}
	if policy == "confirm-destructive" && destructive(verb) && typed != currentContext {
		return fmt.Errorf("type context name %q to confirm %s", currentContext, verb)
	}
	return nil
}

func destructive(verb Verb) bool {
	switch verb {
	case Delete, Scale, Restart, Exec, Edit, Patch:
		return true
	default:
		return false
	}
}
