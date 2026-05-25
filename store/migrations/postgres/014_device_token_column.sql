-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'users'
      AND column_name = 'firebase_token'
  ) AND NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'users'
      AND column_name = 'device_token'
  ) THEN
    ALTER TABLE users RENAME COLUMN firebase_token TO device_token;
  ELSIF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'users'
      AND column_name = 'firebase_token'
  ) AND EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'users'
      AND column_name = 'device_token'
  ) THEN
    UPDATE users
    SET device_token = firebase_token
    WHERE (device_token IS NULL OR device_token = '')
      AND firebase_token IS NOT NULL;
    ALTER TABLE users DROP COLUMN firebase_token;
  ELSIF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'users'
      AND column_name = 'device_token'
  ) THEN
    ALTER TABLE users ADD COLUMN device_token TEXT;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'users'
      AND column_name = 'device_token'
  ) AND NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'users'
      AND column_name = 'firebase_token'
  ) THEN
    ALTER TABLE users RENAME COLUMN device_token TO firebase_token;
  ELSIF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'users'
      AND column_name = 'device_token'
  ) AND EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'users'
      AND column_name = 'firebase_token'
  ) THEN
    UPDATE users
    SET firebase_token = device_token
    WHERE (firebase_token IS NULL OR firebase_token = '')
      AND device_token IS NOT NULL;
    ALTER TABLE users DROP COLUMN device_token;
  END IF;
END $$;
-- +goose StatementEnd
