package search

const schemaSQL = `
CREATE TABLE IF NOT EXISTS search_docs (
    message_id BIGINT PRIMARY KEY,
    chat_id    BIGINT NOT NULL,
    sender_id  BIGINT NOT NULL,
    seq        BIGINT NOT NULL,
    body       TEXT NOT NULL,
    tsv        tsvector
);
CREATE INDEX IF NOT EXISTS idx_search_tsv ON search_docs USING GIN (tsv);
CREATE INDEX IF NOT EXISTS idx_search_chat_seq ON search_docs(chat_id, seq DESC);
`
