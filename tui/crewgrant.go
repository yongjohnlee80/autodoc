package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/mail"
)

// grantAnsweredBody is the mail that tells an agent the user's answer to the grant card its refused
// call asked (editorVerb): what it may now do, or may not, and what to do next, so it never retries
// blind nor waits for nothing.
func grantAnsweredBody(caps []string, allowed bool) (subject, body string) {
	what := strings.Join(caps, ", ")
	if allowed {
		return "Allowed: " + what, fmt.Sprintf("The user allowed %s at the card your refused call asked. "+
			"Call it again now: it is no longer refused for %s.", what, what)
	}
	return "Not allowed: " + what, fmt.Sprintf("The user did not allow %s (the card your refused call asked was "+
		"answered no, or closed). Don't call it again for now; if you need it, say why to the user, "+
		"and they can allow it when it asks again.", what)
}

// grantAnswered mails codename, one of the window's agents, the user's answer to its grant card, as
// the user's own message (userSend: it rings the agent's doorbell, or its fallback delivers it). A
// window holding no scope, or an agent the crew can't reach, says nothing more: the agent's guide
// tells it to try again once the user has answered.
func (h *crewHost) grantAnswered(codename string, caps []string, allowed bool) {
	subject, body := grantAnsweredBody(caps, allowed)
	h.userSend(codename, mail.KindMessage, subject, body, func(err error) {
		if err != nil && !errors.Is(err, errHandedOver) {
			h.c.Notify(h.c.Trf("autodoc.crew.grant_answer_unsent", codename, err.Error()))
		}
	})
}
