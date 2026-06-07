import { LegalLayout } from "./LegalLayout";

export function Terms() {
  return (
    <LegalLayout
      title="Terms of Service"
      updated="June 5, 2026"
      intro="These terms govern Hygur Cloud, our optional managed hosting of Hygur Server. The Hygur app and self-hosted server remain free and local-first — these terms apply only if you subscribe to Hygur Cloud."
    >
      <h2>1. Who provides the service</h2>
      <p>
        Hygur Cloud is provided by <strong>0x0800 SRL</strong>, Chaussée
        Brunehault 702, 4042 Herstal, Belgium (enterprise number BE
        1021.845.609). Contact: <a href="mailto:hello@hygur.com">hello@hygur.com</a>.
      </p>

      <h2>2. The service</h2>
      <p>
        Hygur Cloud is a managed Hygur Server instance, one per account. We run,
        host and update it; you connect the Hygur app to it and keep control of
        your data. Local sources stay on your device — only extracted text is
        pushed to your instance.
      </p>

      <h2>3. Subscription &amp; billing</h2>
      <p>
        The Personal plan is <strong>€29 per month, taxes included</strong>,
        billed monthly through our payment processor Stripe. You can cancel at any
        time; access continues until the end of the paid period. Payments are
        non-refundable except where required by law.
      </p>

      <h2>4. Where your data lives — our commitments</h2>
      <p>
        Hygur Cloud runs <strong>exclusively on servers located in the European
        Union</strong>. AI inference (the LLM and embeddings) runs on{" "}
        <strong>GPU infrastructure located in the European Union</strong>. We do{" "}
        <strong>not use your data to train any model</strong>, and we do not sell
        or share it. Your knowledge base is encrypted at rest; you can export or
        delete it at any time.
      </p>

      <h2>5. Acceptable use</h2>
      <p>
        Don't use Hygur Cloud for unlawful content, to infringe others' rights, or
        to attack or overload the infrastructure. We may suspend an account that
        does, or that puts the shared platform at risk.
      </p>

      <h2>6. Availability</h2>
      <p>
        We aim for high availability but provide the service on a best-effort
        basis during this early phase, without a formal uptime guarantee. We may
        perform maintenance and will keep disruption reasonable.
      </p>

      <h2>7. Liability</h2>
      <p>
        To the extent permitted by law, our liability is limited to the amount you
        paid for the service in the three months preceding the event. Nothing here
        excludes liability that cannot be excluded under Belgian or EU law.
      </p>

      <h2>8. Governing law</h2>
      <p>
        These terms are governed by Belgian law. Mandatory consumer-protection
        rights in your country of residence are unaffected. Data processing is
        described in our <a href="#/privacy">Privacy Policy</a>.
      </p>

      <h2>9. Changes</h2>
      <p>
        We may update these terms as the service evolves; the effective date is
        shown above. Material changes will be communicated before they take effect.
      </p>
    </LegalLayout>
  );
}
