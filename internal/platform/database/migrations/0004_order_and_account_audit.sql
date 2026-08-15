-- 0004_order_and_account_audit.sql
--
-- Adds the two audit trails the back office needs to answer "why?".
--
-- Both are append-only by intent: nothing updates or deletes a row, and neither
-- carries deleted_at. An audit trail that can be quietly rewritten is worth less
-- than no trail at all, because it invites being trusted.
--
-- Why these are separate from what already exists:
--
--   * orders.status is a single column the next write overwrites, so an order
--     cancelled by mistake and one cancelled for fraud are indistinguishable
--     afterwards. order_events, despite the name, records an event discount
--     applied to the basket -- it has event_id and discount, not a state change.
--
--   * users.status is likewise one integer. An unblock erased the fact that a
--     block ever happened.

--
-- Name: order_status_histories; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.order_status_histories (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    order_id uuid NOT NULL,
    -- Empty on the row that records an order being created, which has no
    -- previous status. Nullable rather than NOT NULL DEFAULT '' so the absence
    -- is explicit.
    from_status character varying(255),
    to_status character varying(255) NOT NULL,
    -- Mandatory for CANCELED and STORE_REJECTED, optional elsewhere. Enforced by
    -- internal/modules/order, not by the column: NOT NULL here would also reject
    -- the legitimate empty reason on a happy-path transition.
    reason text,
    -- NULL when no person made the transition -- a payment callback or a
    -- scheduled job. Storing NULL is honest; attributing a machine transition to
    -- whoever happened to be signed in is not.
    actor_id uuid,
    created_at timestamp with time zone
);

ALTER TABLE ONLY public.order_status_histories
    ADD CONSTRAINT order_status_histories_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.order_status_histories
    ADD CONSTRAINT fk_order_status_histories_order FOREIGN KEY (order_id)
    REFERENCES public.orders(id) ON DELETE CASCADE;

-- ON DELETE SET NULL rather than CASCADE: removing a staff account must not
-- erase the record of what they did.
ALTER TABLE ONLY public.order_status_histories
    ADD CONSTRAINT fk_order_status_histories_actor FOREIGN KEY (actor_id)
    REFERENCES public.users(id) ON DELETE SET NULL;

-- The timeline is always read for one order, newest first.
CREATE INDEX idx_order_status_histories_order_created
    ON public.order_status_histories USING btree (order_id, created_at DESC);

CREATE INDEX idx_order_status_histories_actor
    ON public.order_status_histories USING btree (actor_id);

--
-- Name: account_actions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.account_actions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    target_user_id uuid NOT NULL,
    -- NOT NULL: every row here is written by a person through the admin API. A
    -- future automated suspension would need this relaxed, and that change
    -- should be deliberate rather than accidental.
    actor_id uuid NOT NULL,
    action character varying(50) NOT NULL,
    reason text,
    created_at timestamp with time zone
);

ALTER TABLE ONLY public.account_actions
    ADD CONSTRAINT account_actions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.account_actions
    ADD CONSTRAINT fk_account_actions_target FOREIGN KEY (target_user_id)
    REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.account_actions
    ADD CONSTRAINT fk_account_actions_actor FOREIGN KEY (actor_id)
    REFERENCES public.users(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.account_actions
    ADD CONSTRAINT chk_account_actions_action
    CHECK (action IN ('BLOCKED', 'UNBLOCKED'));

CREATE INDEX idx_account_actions_target_created
    ON public.account_actions USING btree (target_user_id, created_at DESC);

CREATE INDEX idx_account_actions_actor
    ON public.account_actions USING btree (actor_id);
