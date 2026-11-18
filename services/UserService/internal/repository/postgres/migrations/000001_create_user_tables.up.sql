CREATE TABLE IF NOT EXISTS user_profiles (
    id               TEXT PRIMARY KEY,
    email            TEXT NOT NULL DEFAULT '',
    display_name     TEXT NOT NULL DEFAULT '',
    role             TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'manager', 'admin')),
    status           TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'blocked')),
    plan             TEXT NOT NULL DEFAULT 'default' CHECK (plan IN ('default', 'pro')),
    plan_expires_at  TIMESTAMPTZ,
    tech_stack       TEXT[] NOT NULL DEFAULT '{}',
    experience       TEXT NOT NULL DEFAULT '',
    location         TEXT NOT NULL DEFAULT '',
    expected_salary  INT NOT NULL DEFAULT 0 CHECK (expected_salary >= 0),
    telegram_chat_id BIGINT UNIQUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_profiles_email ON user_profiles (LOWER(email));
CREATE INDEX IF NOT EXISTS idx_user_profiles_role ON user_profiles (role);

CREATE TABLE IF NOT EXISTS user_notification_settings (
    user_id     TEXT PRIMARY KEY REFERENCES user_profiles (id) ON DELETE CASCADE,
    frequency   TEXT NOT NULL DEFAULT 'instant' CHECK (frequency IN ('instant', 'hourly', 'daily')),
    types       TEXT[] NOT NULL DEFAULT '{new_vacancy,billing,system}',
    muted       BOOLEAN NOT NULL DEFAULT FALSE,
    muted_until TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS user_subscriptions (
    id         BIGSERIAL PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES user_profiles (id) ON DELETE CASCADE,
    keyword    TEXT NOT NULL,
    location   TEXT NOT NULL DEFAULT '',
    min_salary INT NOT NULL DEFAULT 0 CHECK (min_salary >= 0),
    is_active  BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_subscriptions_user ON user_subscriptions (user_id);
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_keyword ON user_subscriptions (keyword) WHERE is_active;

CREATE TABLE IF NOT EXISTS user_saved_vacancies (
    user_id    TEXT NOT NULL REFERENCES user_profiles (id) ON DELETE CASCADE,
    vacancy_id TEXT NOT NULL,
    title      TEXT NOT NULL DEFAULT '',
    company    TEXT NOT NULL DEFAULT '',
    location   TEXT NOT NULL DEFAULT '',
    salary     TEXT NOT NULL DEFAULT '',
    link       TEXT NOT NULL DEFAULT '',
    saved_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, vacancy_id)
);

CREATE TABLE IF NOT EXISTS user_vacancy_views (
    user_id    TEXT NOT NULL REFERENCES user_profiles (id) ON DELETE CASCADE,
    vacancy_id TEXT NOT NULL,
    title      TEXT NOT NULL DEFAULT '',
    company    TEXT NOT NULL DEFAULT '',
    location   TEXT NOT NULL DEFAULT '',
    salary     TEXT NOT NULL DEFAULT '',
    link       TEXT NOT NULL DEFAULT '',
    viewed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, vacancy_id)
);

CREATE INDEX IF NOT EXISTS idx_user_vacancy_views_time ON user_vacancy_views (user_id, viewed_at DESC);

CREATE TABLE IF NOT EXISTS user_telegram_codes (
    code       TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES user_profiles (id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS user_activity (
    id         BIGSERIAL PRIMARY KEY,
    user_id    TEXT NOT NULL,
    actor_id   TEXT NOT NULL,
    action     TEXT NOT NULL,
    meta       JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The trail of a deleted account is kept for the audit, so there is no foreign key here.
CREATE INDEX IF NOT EXISTS idx_user_activity_user ON user_activity (user_id, created_at DESC);
