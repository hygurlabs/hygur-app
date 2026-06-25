import { LegalLayout } from "./LegalLayout";

export function Privacy() {
  return (
    <LegalLayout
      title="Privacy Policy"
      updated="June 20, 2026"
      intro="Hygur is built local-first: the app and the self-hosted Server keep your data on your own machine and send none of it to us. This policy covers the public website and Hygur Cloud, our optional managed service."
    >
      <h2>1. Who we are</h2>
      <p>
        The data controller is <strong>0x0800 SRL</strong>, Chaussée Brunehault
        702, 4042 Herstal, Belgium (enterprise number BE 1021.845.609). For any
        question about your data, write to{" "}
        <a href="mailto:privacy@hygur.ai">privacy@hygur.ai</a>.
      </p>

      <h2>2. What this policy covers</h2>
      <p>
        It covers two things: the public website at hygur.ai, and{" "}
        <strong>Hygur Cloud</strong>, our optional managed hosting of Hygur
        Server. It does <strong>not</strong> cover the Hygur app or a
        self-hosted Server: those run on your own machine or infrastructure,
        index your data locally, and send no personal data to us. If you only
        use the local-first app or self-host the Server, your documents, mail,
        notes and prompts never reach us.
      </p>

      <h2>3. Data we process</h2>
      <p>
        <strong>Public website.</strong> The site is static, needs no account,
        and runs no advertising, analytics or social trackers; fonts are
        self-hosted. Our hosting provider records the technical data needed to
        serve pages — IP address, requested URL, timestamp and user agent — in
        server logs, kept for a limited period for security and delivery, and
        not used to profile you.
      </p>
      <p>
        <strong>Hygur Cloud (if you subscribe).</strong> We process:
      </p>
      <ul>
        <li>
          your account email, collected through Stripe at checkout, and the
          passkey credential that identifies your device;
        </li>
        <li>
          the content you connect or import — extracted text from your mail and
          files, your notes and prompts — and the search vectors and indexes
          derived from it. This is the sensitive core of the service;
        </li>
        <li>
          usage counters (token counts) for billing and quotas, and minimal
          operational and security logs.
        </li>
      </ul>
      <p>
        Payment is handled by <strong>Stripe</strong>; we never store your card
        details.
      </p>

      <h2>4. Purposes and legal bases</h2>
      <ul>
        <li>
          Providing the service (your knowledge assistant): performance of our
          contract with you.
        </li>
        <li>
          Security, abuse prevention and quota enforcement: our legitimate
          interest.
        </li>
        <li>
          Billing and accounting: compliance with a legal obligation and
          performance of the contract.
        </li>
      </ul>
      <p>
        We do <strong>not</strong> use your content to train any model, and we
        do not sell or share it.
      </p>

      <h2>5. Sub-processors and international transfers</h2>
      <p>We rely on a small number of processors:</p>
      <ul>
        <li>
          <strong>Infomaniak</strong> (Switzerland / EU) — AI inference, storage
          and encrypted backups for Hygur Cloud. Your content is processed under
          a no-training commitment.
        </li>
        <li>
          <strong>Stripe</strong> (EU / US) — payment processing. We hold no card
          data.
        </li>
        <li>
          <strong>Hetzner Online GmbH</strong> (Germany, EU) — hosting of the
          instances.
        </li>
      </ul>
      <p>
        Transfers outside the EU are covered: Switzerland benefits from a
        European Commission adequacy decision, and transfers to Stripe rely on
        the Standard Contractual Clauses. The current list of sub-processors is
        kept in our records and updated as it changes.
      </p>

      <h2>6. Security and encryption</h2>
      <p>
        Each customer's space is encrypted at rest with SQLCipher under its own
        dedicated encryption key. Spaces are isolated from one another, with
        separate keys. Sign-in uses passkeys, so we store no passwords. Backups
        are encrypted and kept off-site.
      </p>

      <h2>7. Retention and deletion</h2>
      <p>
        We keep your content while your account is active. When you cancel, your
        space's encryption key is destroyed immediately, which makes the data
        unreadable, and the volume is then purged after 30 days. Encrypted
        backups remain in the backup repository until they expire under our
        retention schedule — 7 daily, 4 weekly and 6 monthly copies — after
        which they are deleted. You can export your data at any time.
      </p>

      <h2>8. Your rights</h2>
      <p>
        Under the GDPR you have the right to access, rectify, erase, restrict
        and port your personal data, and to object to its processing. To
        exercise these rights, write to{" "}
        <a href="mailto:privacy@hygur.ai">privacy@hygur.ai</a>; we respond
        within 30 days. You may also lodge a complaint with the Belgian Data
        Protection Authority (Autorité de protection des données /
        Gegevensbeschermingsautoriteit).
      </p>

      <h2>9. Cookies</h2>
      <p>
        The service uses only essential session and authentication cookies. No
        advertising, analytics or tracking cookies are set, so no consent banner
        is required.
      </p>

      <h2>10. Changes and contact</h2>
      <p>
        We may update this policy as the service evolves; the effective date is
        shown above, and we will notify you of any substantial change. This is
        version 1.0.1. For any question, write to{" "}
        <a href="mailto:privacy@hygur.ai">privacy@hygur.ai</a>.
      </p>
    </LegalLayout>
  );
}
