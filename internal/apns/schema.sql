CREATE TABLE devices (
    device_id SERIAL PRIMARY KEY,
    user_id UUID NOT NULL,
    token TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT now(),
    UNIQUE(user_id, token)
);
