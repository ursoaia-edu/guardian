CREATE ROLE guardian_app LOGIN PASSWORD 'guardian_app';
GRANT CONNECT ON DATABASE guardian TO guardian_app;
GRANT USAGE ON SCHEMA public TO guardian_app;
