-- 0003_permissions.sql
--
-- Adds the permission catalogue and the role -> permission join.
--
-- Until now authorisation was a bare role name compared against a hardcoded
-- list in every module's routes.go, so "who may do what" was spread across
-- fifteen files and could not be changed without a deploy. These two tables let
-- an operator answer that question at runtime.
--
-- The seed below is not free-form: it mirrors internal/modules/role/catalog.go
-- exactly, and TestCatalogMatchesMigrationSeed fails if the two drift. Adding a
-- permission means appending to that list AND to a NEW migration file -- this
-- one is applied in production and the runner rejects an edited migration by
-- checksum.

--
-- Name: permissions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.permissions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    code character varying(100) NOT NULL,
    label character varying(150) NOT NULL,
    permission_group character varying(100) NOT NULL
);

ALTER TABLE ONLY public.permissions
    ADD CONSTRAINT permissions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.permissions
    ADD CONSTRAINT uni_permissions_code UNIQUE (code);

--
-- Name: role_permissions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.role_permissions (
    role_id uuid NOT NULL,
    permission_id uuid NOT NULL
);

-- The pair is the key, so the same grant cannot be recorded twice and a
-- concurrent double submit resolves to one row rather than two.
ALTER TABLE ONLY public.role_permissions
    ADD CONSTRAINT role_permissions_pkey PRIMARY KEY (role_id, permission_id);

CREATE INDEX idx_role_permissions_permission_id
    ON public.role_permissions USING btree (permission_id);

-- Cascading is deliberate. Deleting a role is already refused while users hold
-- it; when it is genuinely removed its grants must go with it, or a later role
-- issued the same id would silently inherit them.
ALTER TABLE ONLY public.role_permissions
    ADD CONSTRAINT fk_role_permissions_role FOREIGN KEY (role_id)
    REFERENCES public.roles(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.role_permissions
    ADD CONSTRAINT fk_role_permissions_permission FOREIGN KEY (permission_id)
    REFERENCES public.permissions(id) ON DELETE CASCADE;

--
-- Seed: the catalogue. Mirrors internal/modules/role/catalog.go.
--

INSERT INTO public.permissions (code, label, permission_group) VALUES
    ('type_product.read', 'Lihat tipe produk', 'Katalog'),
    ('type_product.write', 'Kelola tipe produk', 'Katalog'),
    ('product.read', 'Lihat produk', 'Katalog'),
    ('product.write', 'Kelola produk', 'Katalog'),
    ('shop.read', 'Lihat toko', 'Toko'),
    ('shop.write', 'Kelola toko', 'Toko'),
    ('shop_product.read', 'Lihat produk toko', 'Toko'),
    ('shop_product.write', 'Kelola produk toko', 'Toko'),
    ('cashier.read', 'Lihat kasir', 'Toko'),
    ('cashier.write', 'Kelola kasir', 'Toko'),
    ('order.read', 'Lihat pesanan', 'Pesanan'),
    ('order.write', 'Kelola pesanan', 'Pesanan'),
    ('banner.read', 'Lihat banner', 'Promosi'),
    ('banner.write', 'Kelola banner', 'Promosi'),
    ('event.read', 'Lihat event', 'Promosi'),
    ('event.write', 'Kelola event', 'Promosi'),
    ('voucher.read', 'Lihat voucher', 'Promosi'),
    ('voucher.write', 'Kelola voucher', 'Promosi'),
    ('report_transaction.read', 'Lihat laporan transaksi', 'Laporan'),
    ('report_financial.read', 'Lihat laporan keuangan', 'Laporan'),
    ('user.read', 'Lihat pengguna', 'Pengguna'),
    ('user.write', 'Kelola pengguna', 'Pengguna'),
    ('role.read', 'Lihat role dan akses', 'Sistem'),
    ('role.write', 'Kelola role dan akses', 'Sistem'),
    ('notification.read', 'Lihat notifikasi', 'Sistem'),
    ('notification.write', 'Kirim notifikasi', 'Sistem')
ON CONFLICT (code) DO NOTHING;

--
-- Seed: superadmin holds everything.
--
-- Without this the first thing the new UI would show is a superadmin with no
-- permissions at all, which is exactly the state the service refuses to let an
-- operator create by hand.
--
INSERT INTO public.role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM public.roles r
CROSS JOIN public.permissions p
WHERE LOWER(r.name) = 'superadmin'
ON CONFLICT DO NOTHING;
