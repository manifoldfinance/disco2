CREATE TABLE transactions (
    id UUID PRIMARY KEY,
    account_id UUID NOT NULL,
    card_id UUID, -- optional
    amount BIGINT NOT NULL, -- in cents
    currency TEXT NOT NULL,
    merchant_id UUID, -- optional, can be set after enrichment
    merchant_raw TEXT, -- raw merchant description
    category TEXT, -- optional category
    status TEXT NOT NULL, -- e.g., 'AUTHORIZED','SETTLED','REVERSED'
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE INDEX transactions_account_id_idx ON transactions(account_id);
-- CREATE INDEX transactions_card_id_idx ON transactions(card_id); -- Optional index
