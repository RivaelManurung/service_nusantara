-- 0005_notification_broadcasts.sql
--
-- Records each broadcast once, so the back office can answer "what have we
-- sent?".
--
-- The notifications table cannot: it holds one row per recipient, so a promo to
-- four hundred customers is four hundred rows differing only by user_id.
-- Rebuilding the send from them means grouping by title and a time window,
-- which is guesswork -- two operators sending the same title a minute apart
-- would merge into one send, and a personalised body would split one send into
-- many.
--
-- It also stores what the recipient rows never can: how many accounts the
-- audience resolved to, whether push was asked for, how many devices took it,
-- and who pressed the button.

CREATE TABLE public.notification_broadcasts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,

    title character varying(255) NOT NULL,
    body text,
    channel character varying(20) NOT NULL,
    type character varying(30),
    target_type character varying(30),
    target_route character varying(255),

    -- How the operator described the audience, kept beside the resolved count:
    -- "semua pelanggan" and "412 orang" answer different questions later.
    audience_mode character varying(20) NOT NULL,

    -- recipient_count is what the audience resolved to; saved_count is how many
    -- inbox rows were actually written. They differ only when a write partly
    -- failed, which is precisely when somebody needs to see it.
    recipient_count integer NOT NULL DEFAULT 0,
    saved_count bigint NOT NULL DEFAULT 0,

    push_requested boolean NOT NULL DEFAULT false,
    push_enabled boolean NOT NULL DEFAULT false,
    push_sent integer NOT NULL DEFAULT 0,
    push_failed integer NOT NULL DEFAULT 0,
    push_error text,

    actor_id uuid NOT NULL,
    created_at timestamp with time zone
);

ALTER TABLE ONLY public.notification_broadcasts
    ADD CONSTRAINT notification_broadcasts_pkey PRIMARY KEY (id);

-- RESTRICT rather than CASCADE: deleting a staff account must not erase the
-- record of the promos they sent.
ALTER TABLE ONLY public.notification_broadcasts
    ADD CONSTRAINT fk_notification_broadcasts_actor FOREIGN KEY (actor_id)
    REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.notification_broadcasts
    ADD CONSTRAINT chk_notification_broadcasts_audience
    CHECK (audience_mode IN ('ALL', 'USERS', 'SEGMENT'));

ALTER TABLE ONLY public.notification_broadcasts
    ADD CONSTRAINT chk_notification_broadcasts_channel
    CHECK (channel IN ('TRANSAKSI', 'PROMO'));

-- The list is always "most recent first", which is the only way this is read.
CREATE INDEX idx_notification_broadcasts_created
    ON public.notification_broadcasts USING btree (created_at DESC);

CREATE INDEX idx_notification_broadcasts_actor
    ON public.notification_broadcasts USING btree (actor_id);
