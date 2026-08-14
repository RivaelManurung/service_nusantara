-- 0002_reviews.sql
--
-- Customer product reviews.
--
-- 0001_init.sql is already applied in every environment and the runner compares
-- checksums, so a new table ships as its own file rather than as an edit to the
-- initial dump.
--
-- The shape mirrors what AutoMigrate produces for model.Review, so a
-- development database built either way ends up identical.

--
-- Name: reviews; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.reviews (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    product_id uuid NOT NULL,
    order_id uuid,
    rating bigint NOT NULL,
    comment text,
    status bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone,
    updated_at timestamp with time zone,
    deleted_at timestamp with time zone,
    -- The service clamps the rating too; the constraint is what makes the rule
    -- true for anything that writes to the table without going through it.
    CONSTRAINT reviews_rating_range CHECK (rating >= 1 AND rating <= 5)
);

--
-- Name: reviews reviews_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reviews
    ADD CONSTRAINT reviews_pkey PRIMARY KEY (id);

--
-- Name: idx_reviews_deleted_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_reviews_deleted_at ON public.reviews USING btree (deleted_at);

--
-- Name: idx_reviews_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_reviews_user_id ON public.reviews USING btree (user_id);

--
-- Name: idx_reviews_product_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_reviews_product_id ON public.reviews USING btree (product_id);

--
-- Name: idx_reviews_order_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_reviews_order_id ON public.reviews USING btree (order_id);

--
-- Name: idx_reviews_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_reviews_status ON public.reviews USING btree (status);

--
-- Name: reviews fk_reviews_user; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reviews
    ADD CONSTRAINT fk_reviews_user FOREIGN KEY (user_id) REFERENCES public.users(id);

--
-- Name: reviews fk_reviews_product; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reviews
    ADD CONSTRAINT fk_reviews_product FOREIGN KEY (product_id) REFERENCES public.products(id);

--
-- Name: reviews fk_reviews_order; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reviews
    ADD CONSTRAINT fk_reviews_order FOREIGN KEY (order_id) REFERENCES public.orders(id) ON DELETE CASCADE;
