-- Example only: runs once, when the Compose example creates an empty PostgreSQL volume.
-- Each service has its own database; there are no cross-database foreign keys. The `tripmap`
-- database is created by POSTGRES_DB's default. Use a separate role per service with its own
-- password in a real deployment instead of the single POSTGRES_USER.
CREATE DATABASE bff;
CREATE DATABASE usersrv;
CREATE DATABASE metro;
