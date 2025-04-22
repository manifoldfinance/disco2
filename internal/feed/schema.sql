CREATE TABLE feed_items (
    id UUID PRIMARY KEY,
    account_id UUID NOT NULL,
    type TEXT NOT NULL,
    content TEXT,
    ref_id TEXT,
    timestamp TIMESTAMP NOT NULL DEFAULT now()
);

CREATE INDEX feed_items_account_ts_idx ON feed_items(account_id, timestamp DESC);
