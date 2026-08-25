package eventbus

const streamName = "SYNCAPP_EVENTS"

// durableSubjects are persisted in the JetStream stream (prefixes).
var durableSubjects = []string{"message.>", "notify.>"}
