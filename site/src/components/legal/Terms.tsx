import { LegalLayout } from "./LegalLayout";

export function Terms() {
  return (
    <LegalLayout
      title="Terms of Service"
      updated="June 9, 2026"
      intro="These terms govern Hygur Cloud, our optional managed hosting of Hygur Server. The Hygur app and self-hosted server remain free and local-first — these terms apply only if you subscribe to Hygur Cloud."
    >
      <h2>1. Who provides the service</h2>
      <p>
        Hygur Cloud is provided by <strong>0x0800 SRL</strong>, Chaussée
        Brunehault 702, 4042 Herstal, Belgium (enterprise number BE
        1021.845.609). For any question about these terms, contact us at{" "}
        <a href="mailto:hello@hygur.com">hello@hygur.com</a>.
      </p>

      <h2>2. The service</h2>
      <p>
        Hygur Cloud is a managed Hygur Server instance, one per account. We
        provision, host, and update it on your behalf; you connect the Hygur
        app to it and retain full control of your data. The service includes
        the server runtime, encrypted storage for your knowledge base, and
        access to EU-based AI inference (LLM and embeddings).
      </p>
      <p>
        Hygur is <strong>local-first by design</strong>. Your original emails
        and files stay on your device or in your own mailbox &mdash; Hygur
        Cloud stores only the text it extracts from those sources and the
        notes, summaries, and briefs you create within the app. No original
        attachments or raw mailbox data are transmitted to or stored on our
        servers.
      </p>

      <h2>3. Subscription &amp; billing</h2>
      <p>
        The Hygur Cloud Personal plan is billed monthly through{" "}
        <strong>Stripe</strong>. The standard price is{" "}
        <strong>&euro;35 per month, taxes included</strong>.
      </p>
      <p>
        <strong>Launch offer &mdash; first 2,000 subscribers.</strong> If you
        subscribe while the launch offer is active, your monthly price is{" "}
        <strong>&euro;29.90 per month, taxes included</strong>, locked for the
        lifetime of your subscription. This grandfathered rate applies as long
        as your subscription remains continuously active, even after the
        standard price changes. The offer closes once 2,000 active
        subscriptions have been created; subsequent subscribers pay the
        standard price.
      </p>
      <p>
        You can cancel at any time from your account settings. Access
        continues until the end of the current paid period; no prorated
        refund is issued for the remaining days of that period, except where
        required by applicable law. Payments are otherwise non-refundable.
      </p>

      <h2>4. Right of withdrawal &amp; its waiver</h2>
      <p>
        As a consumer contracting at a distance within the European Union, you
        normally have a <strong>14-day right of withdrawal</strong> from the
        date of subscription, without giving any reason, under Directive
        2011/83/EU on consumer rights.
      </p>
      <p>
        <strong>
          However, by completing your subscription you expressly request that
          Hygur Cloud begin supplying the service immediately, before the
          14-day withdrawal period expires.
        </strong>{" "}
        You acknowledge that, once the service has begun and is being fully
        performed, or once the digital content has been supplied to you, you{" "}
        <strong>lose the right of withdrawal</strong> in accordance with
        Article 16(a) and Article 16(m) of Directive 2011/83/EU.
      </p>
      <p>
        In practice this means: as soon as your Hygur Cloud instance is
        provisioned and accessible &mdash; which happens immediately after
        successful payment &mdash; the right of withdrawal no longer applies.
        We collect your explicit consent to this effect at checkout. This
        clause does not affect any other statutory rights you hold under
        Belgian or EU consumer law.
      </p>

      <h2>5. Non-payment &amp; suspension</h2>
      <p>
        If a scheduled payment fails, Stripe will retry according to its
        standard dunning schedule. During the retry period your instance may
        be <strong>suspended</strong> (paused): it remains provisioned but is
        not accessible. Access resumes automatically once a payment succeeds.
      </p>
      <p>
        If all retries are exhausted or you cancel your subscription, access
        ends and your instance enters a <strong>30-day retention window</strong>{" "}
        (see Section 6 below). During that window you can reactivate or export
        your data from the app at any time; after it, the instance is
        decommissioned and your data is irrecoverably deleted.
      </p>

      <h2>6. Your data, encryption, retention &amp; deletion</h2>
      <p>
        <strong>Encryption at rest.</strong> Your knowledge base is encrypted
        at rest using a cryptographic key that is <strong>unique to your
        instance</strong>. We do not hold a copy of your personal passphrase.
        You can export your knowledge base at any time as an encrypted archive
        that you unlock with a passphrase of your own choosing &mdash; Hygur
        never knows that passphrase.
      </p>
      <p>
        <strong>EU hosting &amp; inference.</strong> Hygur Cloud runs
        exclusively on servers located in the{" "}
        <strong>European Union</strong> (Hetzner, Germany). AI inference &mdash;
        the language model and embedding computations &mdash; also runs on{" "}
        <strong>GPU infrastructure located in the European Union</strong>. Your
        data is never processed outside the EU without your prior consent.
      </p>
      <p>
        <strong>No training on your data.</strong> We do{" "}
        <strong>not</strong> use your data to train, fine-tune, or evaluate
        any model, and we do not sell or share it with third parties for their
        commercial purposes.
      </p>
      <p>
        <strong>Retention window.</strong> When your subscription ends
        (cancellation or definitive non-payment), your instance enters a{" "}
        <strong>30-day retention window</strong>. During this window you can
        reactivate your subscription or export your data. After 30 days, the
        instance-specific encryption key is <strong>destroyed</strong>,
        rendering the stored data cryptographically unrecoverable, and the
        physical data is removed from our systems. Deletion is permanent and
        cannot be reversed.
      </p>
      <p>
        <strong>What Hygur stores &mdash; and what it does not.</strong> As
        noted in Section 2, Hygur Cloud stores only the text extracted from
        your sources and the content you create in the app (notes, briefs,
        summaries). Your original files and emails remain on your device or
        in your own mailbox. How we handle personal data more broadly is
        described in our <a href="#/privacy">Privacy Policy</a>.
      </p>

      <h2>7. Acceptable use</h2>
      <p>
        You may use Hygur Cloud only for lawful purposes and in accordance
        with these terms. You must not:
      </p>
      <ul>
        <li>
          process, store, or transmit content that is unlawful, abusive,
          defamatory, or that infringes a third party&apos;s intellectual
          property or privacy rights;
        </li>
        <li>
          attempt to probe, scan, or test the vulnerability of our
          infrastructure, or circumvent any security or authentication
          measures;
        </li>
        <li>
          use the service in a way that degrades availability for other
          users, including sending abusive volumes of requests; or
        </li>
        <li>
          resell, sublicense, or otherwise make the service available to
          third parties without our written consent.
        </li>
      </ul>
      <p>
        We may suspend or terminate an account that breaches this section, or
        that presents an imminent risk to the shared platform, with or without
        prior notice depending on the severity of the situation.
      </p>

      <h2>8. Availability</h2>
      <p>
        We aim for high availability but provide the service on a
        best-effort basis during this early phase, without a formal uptime
        guarantee or service-level agreement. We may perform scheduled or
        emergency maintenance; we will endeavour to minimise disruption and
        to notify you in advance of planned windows where feasible.
      </p>

      <h2>9. Intellectual property</h2>
      <p>
        Hygur Cloud and the Hygur software are owned by 0x0800 SRL and
        protected by copyright and other intellectual property laws. These
        terms grant you a limited, non-exclusive, non-transferable right to
        access and use the service for your personal or internal business
        purposes. All other rights are reserved.
      </p>
      <p>
        You retain all rights in the content and data you bring to or create
        within Hygur Cloud. By using the service you grant us only the limited
        technical rights necessary to store, process, and return your data to
        you.
      </p>

      <h2>10. Liability</h2>
      <p>
        To the extent permitted by applicable law, our total liability to you
        for any claim arising out of or in connection with these terms or the
        service is limited to the amounts you actually paid us in the{" "}
        <strong>three calendar months immediately preceding the event</strong>{" "}
        giving rise to the claim.
      </p>
      <p>
        We are not liable for indirect, incidental, or consequential loss,
        loss of profit, or loss of data arising from your use of the service,
        to the extent that exclusion is permitted by law.
      </p>
      <p>
        Nothing in these terms excludes or limits liability that cannot be
        excluded or limited under Belgian or EU law, including liability for
        death or personal injury caused by negligence, fraud, or fraudulent
        misrepresentation, or statutory rights that cannot be waived by
        contract.
      </p>

      <h2>11. Governing law &amp; disputes</h2>
      <p>
        These terms are governed by and construed in accordance with{" "}
        <strong>Belgian law</strong>, without regard to its conflict-of-law
        rules. If you are a consumer, mandatory consumer-protection rights
        granted by the law of your country of habitual residence are
        unaffected by this choice of law.
      </p>
      <p>
        Any dispute arising out of or in connection with these terms shall be
        submitted to the exclusive jurisdiction of the courts of{" "}
        <strong>Li&egrave;ge, Belgium</strong>, unless mandatory rules in your
        country of residence require otherwise. EU consumers may also use the
        European Commission&apos;s Online Dispute Resolution platform at{" "}
        <a
          href="https://ec.europa.eu/consumers/odr"
          target="_blank"
          rel="noopener noreferrer"
        >
          ec.europa.eu/consumers/odr
        </a>
        .
      </p>

      <h2>12. Changes to these terms</h2>
      <p>
        We may update these terms as the service evolves. The effective date
        shown at the top of this page reflects the most recent revision. We
        will notify you of any material changes at least 30 days before they
        take effect, by a prominent notice within the app or service. If
        you do not accept the revised terms, you may cancel your subscription
        before the new terms come into force. Continued use of the service
        after that date constitutes acceptance of the revised terms.
      </p>

      <h2>13. Contact</h2>
      <p>
        For questions about these terms, billing, or your account, contact{" "}
        <a href="mailto:hello@hygur.com">hello@hygur.com</a>. For
        data-protection matters, see our{" "}
        <a href="#/privacy">Privacy Policy</a> or write to{" "}
        <a href="mailto:privacy@hygur.com">privacy@hygur.com</a>.
      </p>
    </LegalLayout>
  );
}
