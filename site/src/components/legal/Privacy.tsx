import { LegalLayout } from "./LegalLayout";

export function Privacy() {
  return (
    <LegalLayout
      title="Privacy Policy"
      updated="June 3, 2026"
      intro="Hygur is built local-first. This policy covers the public Hygur website — not the app or server, which keep your data on your own machine and infrastructure."
    >
      <h2>1. Who we are</h2>
      <p>
        This website is operated by <strong>0x0800 SRL</strong>, Chaussée
        Brunehault 702, 4042 Herstal, Belgium (enterprise number BE
        1021.845.609), the data controller for the limited processing described
        below. You can reach us at{" "}
        <a href="mailto:privacy@hygur.com">privacy@hygur.com</a>.
      </p>

      <h2>2. What this policy covers</h2>
      <p>
        This policy applies to the public website at hygur.com. It does{" "}
        <strong>not</strong> cover the Hygur app or the Hygur Server: those run on
        your own machine or infrastructure, index your data locally, and send no
        personal data to us. Your documents, mail, notes and prompts never reach
        our servers.
      </p>

      <h2>3. Data this website collects</h2>
      <p>
        The website is a static page. It requires no account, runs no
        advertising and embeds no third-party analytics or social trackers.
        Fonts are self-hosted, so loading the page makes no calls to third-party
        font or CDN providers.
      </p>
      <p>
        Like any website, our hosting provider processes the technical data
        needed to serve pages — such as your IP address, the requested URL,
        timestamp and user agent — in server logs, for security and to deliver
        the site. These logs are kept for a limited period and are not used to
        profile you.
      </p>

      <h2>4. Cookies &amp; tracking</h2>
      <p>
        This website sets no advertising, analytics or tracking cookies. No
        consent banner is required because no non-essential cookies are used.
      </p>

      <h2>5. Hosting &amp; processors</h2>
      <p>
        The website and its services are hosted by{" "}
        <strong>Hetzner Online GmbH</strong> (Industriestr. 25, 91710
        Gunzenhausen, Germany), within the European Union. As our hosting
        provider, Hetzner acts as a processor and may handle the technical
        server logs described above on our behalf.
      </p>

      <h2>6. Hygur Cloud (optional managed service)</h2>
      <p>
        If you subscribe to <strong>Hygur Cloud</strong>, we host a Hygur Server
        instance for you and act as a processor for the data you choose to push to
        it (extracted text from your files and mail, your notes and prompts). It
        runs <strong>exclusively on servers in the European Union</strong>{" "}
        (Hetzner, Germany); your knowledge base is{" "}
        <strong>encrypted at rest</strong>; and AI inference runs on{" "}
        <strong>GPU infrastructure located in the European Union</strong>. We{" "}
        <strong>never use your data to train any model</strong> and never sell or
        share it. Payments are processed by <strong>Stripe</strong>; we do not
        store your card details. You can export or delete your instance's data at
        any time. The service terms are in our{" "}
        <a href="#/terms">Terms of Service</a>.
      </p>

      <h2>7. Your rights</h2>
      <p>
        Under the GDPR you have the right to access, rectify, erase, restrict
        and port your personal data, and to object to its processing. To
        exercise these rights, contact{" "}
        <a href="mailto:privacy@hygur.com">privacy@hygur.com</a>. You also have
        the right to lodge a complaint with the Belgian Data Protection
        Authority (Autorité de protection des données / Gegevensbeschermings­autoriteit).
      </p>

      <h2>8. Changes to this policy</h2>
      <p>
        We may update this policy as the service evolves. The effective date is
        shown at the top of this page.
      </p>

      <h2>9. Contact</h2>
      <p>
        For any question about this policy or your data, write to{" "}
        <a href="mailto:privacy@hygur.com">privacy@hygur.com</a>.
      </p>
    </LegalLayout>
  );
}
