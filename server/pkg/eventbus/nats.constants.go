package eventbus

const streamName = "SYNAPSE_EVENTS"

// durableSubjects are persisted in the JetStream stream (prefixes).
var durableSubjects = []string{"message.>", "notify.>"}
