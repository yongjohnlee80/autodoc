package tui

import "strings"

// Copies of the agents' test fakes agentmessaging_own_test.go declares, for the host's cells.

// settleRounds bounds settleMessaging's rounds of the crew host's timers: the doorbell's asks again
// need userSendTries of them, and the scope's renewal re-arms itself every round, so the timers
// never run out on their own.
const settleRoundsCopy = userSendTries + 2

func hasNoteCopy(notes []string, prefix string) bool {
	for _, n := range notes {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}
