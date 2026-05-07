CREATE TABLE users (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    email             citext      NOT NULL UNIQUE,
    email_verified_at timestamptz,
    password_hash     text        NOT NULL,
    name              text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   bytea       NOT NULL UNIQUE,
    ip_address   inet,
    user_agent   text,
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user_id_idx     ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx  ON sessions (expires_at);

CREATE TABLE email_tokens (
    id         uuid             PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid             REFERENCES users(id) ON DELETE CASCADE,
    email      citext           NOT NULL,
    token_hash bytea            NOT NULL UNIQUE,
    kind       email_token_kind NOT NULL,
    expires_at timestamptz      NOT NULL,
    used_at    timestamptz,
    created_at timestamptz      NOT NULL DEFAULT now()
);
CREATE INDEX email_tokens_email_kind_idx ON email_tokens (email, kind);
CREATE INDEX email_tokens_user_id_idx    ON email_tokens (user_id);
