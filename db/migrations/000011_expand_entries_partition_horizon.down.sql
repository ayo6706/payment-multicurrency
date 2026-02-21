DO $$
DECLARE
  r RECORD;
BEGIN
  FOR r IN
    SELECT tablename
    FROM pg_tables
    WHERE schemaname = 'public'
      AND tablename ~ '^entries_y[0-9]{4}m[0-9]{2}$'
  LOOP
    EXECUTE format('DROP TABLE IF EXISTS %I;', r.tablename);
  END LOOP;
END $$;
