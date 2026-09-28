package eventbus

import (
	"strings"
	"testing"
)

/*
 * The NATS bus itself needs a server, so its read/write paths live in the
 * integration test that skips without one. These two helpers do not: they decide
 * which subjects are PERSISTED and what a consumer is called, and both decisions
 * are silent when wrong.
 *
 * A subject that stops being durable loses events on a consumer restart — the
 * outbox relay publishes `message.created` and the delivery path reads it, so a
 * misclassification there is lost messages with nothing logged. A durable name
 * that collides makes two different subscriptions share one consumer position,
 * so each sees roughly half the stream and neither notices.
 */

// ------------------------------------------------------------------ isDurable

func TestMessageAndNotifySubjectsArePersisted(t *testing.T) {
	/*
	 * These are the two families that must survive a restart. `message.*` carries
	 * the durable write the outbox relay staged — dropping one is a message that
	 * never arrives — and `notify.push` is the job that wakes a phone, which is
	 * worthless if it evaporates while the worker is redeploying.
	 */
	for _, subject := range []string{
		SubjMessageCreated,
		SubjMessageEdited,
		SubjMessageDeleted,
		SubjMessageRead,
		SubjReaction,
		SubjNotifyPush,
	} {
		if !isDurable(subject) {
			t.Errorf("%q is not persisted; a consumer restart would lose it", subject)
		}
	}
}

func TestEphemeralSubjectsAreNotPersisted(t *testing.T) {
	/*
	 * The opposite mistake is quieter but worse at scale. Typing and presence flip
	 * on every keystroke and every connect; persisting them would write the
	 * highest-churn signals in the system to disk and replay a backlog of stale
	 * "is typing" to a consumer that just restarted.
	 */
	for _, subject := range []string{
		SubjTyping,
		SubjPresence,
		SubjCallState,
		SubjPollState,
		SubjPinned,
	} {
		if isDurable(subject) {
			t.Errorf("%q is persisted; an ephemeral signal would be replayed after a restart", subject)
		}
	}
}

func TestEveryDeclaredSubjectIsClassifiedDeliberately(t *testing.T) {
	// Not an assertion about which side a subject falls on — just that every one
	// of them was considered. A new subject silently defaulting to ephemeral is
	// how a durable event starts getting dropped.
	all := map[string]bool{
		SubjMessageCreated: true,
		SubjMessageEdited:  true,
		SubjMessageDeleted: true,
		SubjMessageRead:    true,
		SubjReaction:       true,
		SubjNotifyPush:     true,
		SubjCallState:      false,
		SubjPollState:      false,
		SubjPinned:         false,
		SubjTyping:         false,
		SubjPresence:       false,
	}

	for subject, wantDurable := range all {
		if got := isDurable(subject); got != wantDurable {
			t.Errorf("%q durable = %v, want %v", subject, got, wantDurable)
		}
	}
}

func TestDurableClassificationMatchesTheStreamsSubjects(t *testing.T) {
	/*
	 * `durableSubjects` is what the stream is provisioned with; `isDurable` is what
	 * the publisher consults. If the two disagreed, a subject could be classified
	 * durable and published to a stream that does not capture it — accepted, and
	 * then gone.
	 */
	for _, pattern := range durableSubjects {
		prefix := strings.TrimSuffix(pattern, ">")
		probe := prefix + "something"

		if !isDurable(probe) {
			t.Errorf("the stream captures %q but isDurable(%q) is false", pattern, probe)
		}
	}
}

func TestAnUnknownSubjectIsTreatedAsEphemeral(t *testing.T) {
	// The safe default: an unrecognised subject goes over core NATS, which is
	// lossy but cheap. Defaulting the other way would provision a consumer for
	// every typo'd subject and retain it forever.
	for _, subject := range []string{"", "chat.something-new", "user.something", "messages.created"} {
		if isDurable(subject) {
			t.Errorf("%q was classified durable", subject)
		}
	}
}

func TestDurabilityIsDecidedByPrefixNotBySubstring(t *testing.T) {
	// "x.message.created" contains "message." but is not in the family; matching
	// on a substring would capture unrelated subjects into the stream.
	if isDurable("x.message.created") {
		t.Error("a subject merely containing the prefix was classified durable")
	}
	if isDurable("chat.notify.push") {
		t.Error("a subject merely containing the prefix was classified durable")
	}
}

// ---------------------------------------------------------------- durableName

func TestDurableNameIsUniquePerQueueAndSubject(t *testing.T) {
	/*
	 * A JetStream consumer owns a position in the stream. Two subscriptions
	 * sharing a name share that position, so each receives roughly half the
	 * events and neither can tell — the symptom is "some messages just do not
	 * arrive", intermittently, under load.
	 */
	seen := map[string]string{}
	for _, queue := range []string{"fanout", "notify", "search"} {
		for _, subject := range []string{
			SubjMessageCreated, SubjMessageEdited, SubjMessageDeleted,
			SubjMessageRead, SubjReaction, SubjNotifyPush,
		} {
			name := durableName(queue, subject)
			key := queue + "|" + subject
			if previous, clash := seen[name]; clash {
				t.Errorf("%s and %s both produce the durable name %q", previous, key, name)
			}
			seen[name] = key
		}
	}
}

func TestDurableNameSeparatesQueuesOnTheSameSubject(t *testing.T) {
	// Two services consuming the same subject are different consumer groups and
	// must each get every event — a shared name would split the stream between
	// them.
	if durableName("fanout", SubjMessageCreated) == durableName("search", SubjMessageCreated) {
		t.Error("two queue groups on one subject share a consumer")
	}
}

func TestDurableNameSeparatesSubjectsInOneQueue(t *testing.T) {
	if durableName("fanout", SubjMessageCreated) == durableName("fanout", SubjMessageDeleted) {
		t.Error("two subjects in one queue share a consumer position")
	}
}

func TestDurableNameSubstitutesADefaultQueue(t *testing.T) {
	// An unnamed subscription still needs a stable consumer; an empty prefix
	// would make the name start with "_" and collide with anything else unnamed
	// in a way that is hard to read in the NATS console.
	name := durableName("", SubjMessageCreated)

	if name == "" || strings.HasPrefix(name, "_") {
		t.Errorf("durable name for an unnamed queue = %q", name)
	}
}

func TestDurableNameContainsNoCharacterNATSRejects(t *testing.T) {
	/*
	 * NATS rejects a durable name containing `.`, `*`, `>` or a space — and it
	 * rejects it at SUBSCRIBE time, which is process startup. The replacer exists
	 * for exactly that, and a subject with a wildcard is the realistic input: the
	 * bus supports trailing-wildcard subscriptions.
	 */
	for _, subject := range []string{
		"message.created",
		"message.*",
		"message.>",
		"a subject with spaces",
		"notify.push",
	} {
		name := durableName("fanout", subject)
		for _, bad := range []string{".", "*", ">", " "} {
			if strings.Contains(name, bad) {
				t.Errorf("durable name %q for subject %q contains %q, which NATS rejects",
					name, subject, bad)
			}
		}
	}
}

func TestDurableNameKeepsWildcardsDistinguishable(t *testing.T) {
	// `message.*` and `message.>` are different subscriptions; mapping both to the
	// same name would make one silently take over the other's position.
	star := durableName("fanout", "message.*")
	all := durableName("fanout", "message.>")

	if star == all {
		t.Errorf("the two wildcard forms collapse to one durable name: %q", star)
	}
}

func TestDurableNameIsStableAcrossRestarts(t *testing.T) {
	/*
	 * The whole point of a durable consumer is that it resumes where it stopped.
	 * A name derived from anything non-deterministic — a timestamp, a random id —
	 * would provision a fresh consumer on every restart and replay the stream
	 * from its start.
	 */
	first := durableName("fanout", SubjMessageCreated)
	for range 5 {
		if got := durableName("fanout", SubjMessageCreated); got != first {
			t.Fatalf("durable name changed between calls: %q then %q", first, got)
		}
	}
}

func TestDurableNameNamesBothHalvesForAnOperator(t *testing.T) {
	// It shows up in `nats consumer ls`; a name that did not say which queue and
	// which subject it belongs to would make a stuck consumer unidentifiable.
	name := durableName("fanout", SubjMessageCreated)

	if !strings.Contains(name, "fanout") {
		t.Errorf("durable name %q does not name its queue", name)
	}
	if !strings.Contains(name, "message") || !strings.Contains(name, "created") {
		t.Errorf("durable name %q does not name its subject", name)
	}
}
