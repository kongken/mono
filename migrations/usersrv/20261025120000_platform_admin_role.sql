-- Modify "user_roles" table: admit the platform role "ozid-admin" next to "metro-admin".
ALTER TABLE "user_roles" DROP CONSTRAINT "user_roles_role", ADD CONSTRAINT "user_roles_role" CHECK ((role)::text = ANY ((ARRAY['metro-admin'::character varying, 'ozid-admin'::character varying])::text[]));
