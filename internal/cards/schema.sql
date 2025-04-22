CREATE TABLE cards (
    card_id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('ACTIVE','INACTIVE','FROZEN','CLOSED')),
    pan_hash TEXT, -- store hashed PAN or token; allow null if not stored
    last_four TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP
);

CREATE INDEX cards_user_id_idx ON cards(user_id);
