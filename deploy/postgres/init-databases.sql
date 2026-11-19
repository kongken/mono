-- Example only: runs once, when the Compose example creates an empty PostgreSQL volume.
-- bff, usersrv and metro have their own databases; there are no cross-database foreign keys. The
-- `tripmap` database is created by POSTGRES_DB's default and is also the database Timeline uses:
-- Timeline and Tripmap share owner/ref foreign keys and the mutation-receipt table, so they must not
-- be split (docs/backend.md, "Timeline 与 Tripmap 共享迁移"). Use a separate role per service with its own
-- password in a real deployment instead of the single POSTGRES_USER.
CREATE DATABASE bff;
CREATE DATABASE usersrv;
CREATE DATABASE metro;
