import { LegalLayout } from "./LegalLayout";

export function LegalNotice() {
  return (
    <LegalLayout
      title="Legal Notice"
      updated="June 3, 2026"
      intro="Legal information and imprint for the Hygur website and services."
    >
      <h2>1. Site publisher</h2>
      <p>This website and the Hygur products are published by:</p>
      <address>
        <strong>0x0800 SRL</strong>
        <br />
        Private limited liability company (SRL/BV) under Belgian law
        <br />
        Registered office: Chaussée Brunehault 702, 4042 Herstal, Belgium
        <br />
        Enterprise number (BCE/KBO): BE 1021.845.609
        <br />
        VAT number: BE 1021.845.609
        <br />
        Contact: <a href="mailto:legal@hygur.com">legal@hygur.com</a>
      </address>
      <p>The publisher is represented by its manager.</p>

      <h2>2. Hosting</h2>
      <p>
        The Hygur website (frontend) and its services (backend) are currently
        hosted by:
      </p>
      <address>
        <strong>Hetzner Online GmbH</strong>
        <br />
        Industriestr. 25, 91710 Gunzenhausen, Germany
        <br />
        Commercial register: Amtsgericht Ansbach, HRB 6089
        <br />
        VAT ID: DE 812871812
        <br />
        Web: <a href="https://www.hetzner.com" target="_blank" rel="noreferrer">hetzner.com</a>
      </address>

      <h2>3. Intellectual property</h2>
      <p>
        The content of this website (text, images, logos, trademarks, structure
        and design) is the exclusive property of 0x0800 SRL or its partners. The
        <strong> Hygur</strong> name and logo are trademarks of 0x0800 SRL; their
        use without prior written authorisation is prohibited.
      </p>
      <p>
        Hygur Server is open-source software distributed under the GNU Affero
        General Public License v3.0 (AGPL-3.0). Use, copying and modification of
        that source code are governed by the terms of that licence. Any other
        reproduction or representation of this website or its content, in whole
        or in part, without prior written authorisation from 0x0800 SRL, is
        prohibited.
      </p>

      <h2>4. Liability</h2>
      <p>
        0x0800 SRL strives to keep the information published on this website
        accurate and up to date, but cannot guarantee its accuracy,
        completeness or timeliness. 0x0800 SRL therefore declines any liability
        for inaccuracies or omissions, and for any damage resulting from
        fraudulent third-party access leading to alteration of the information
        made available here.
      </p>

      <h2>5. External links</h2>
      <p>
        This website may contain hyperlinks to third-party websites, including
        source-code repositories. 0x0800 SRL has no control over those websites
        and accepts no responsibility for their content.
      </p>

      <h2>6. Governing law</h2>
      <p>
        This legal notice is governed by Belgian law. In the event of a dispute,
        and after any attempt to reach an amicable solution has failed, the
        Belgian courts shall have sole jurisdiction.
      </p>

      <h2>7. Personal data</h2>
      <p>
        Under the General Data Protection Regulation (GDPR) and Belgian data
        protection law, you have rights of access, rectification, erasure and
        portability over your personal data. For details on how data is handled,
        see our <a href="#/privacy">Privacy Policy</a>. For any data-related
        request, contact <a href="mailto:privacy@hygur.com">privacy@hygur.com</a>.
      </p>
    </LegalLayout>
  );
}
