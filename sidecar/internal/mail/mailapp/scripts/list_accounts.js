ObjC.import('Foundation');

const Mail = Application("Mail");
const accounts = Mail.accounts();
const out = [];

for (let i = 0; i < accounts.length; i++) {
  const acc = accounts[i];
  let mailboxNames = [];
  try {
    mailboxNames = acc.mailboxes.name();
  } catch (e) {
    // some account types don't expose mailboxes; skip
  }
  out.push({
    id: acc.id(),
    name: acc.name(),
    fullName: (function () { try { return acc.fullName(); } catch (e) { return ""; } })(),
    emailAddresses: (function () { try { return acc.emailAddresses(); } catch (e) { return []; } })(),
    mailboxNames: mailboxNames || []
  });
}

JSON.stringify(out);
