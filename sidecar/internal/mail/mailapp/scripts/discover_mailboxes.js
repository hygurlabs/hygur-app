ObjC.import('Foundation');

function getArgs() {
  const env = $.NSProcessInfo.processInfo.environment;
  const raw = ObjC.unwrap(env.objectForKey("HYGUR_JXA_ARGS")) || "{}";
  return JSON.parse(raw);
}

const args = getArgs(); // { accountId: string }
const Mail = Application("Mail");

let acct = null;
const accts = Mail.accounts();
for (let i = 0; i < accts.length; i++) {
  if (accts[i].id() === args.accountId) { acct = accts[i]; break; }
}
if (!acct) throw new Error("account not found: " + args.accountId);

const out = [];
const mailboxes = acct.mailboxes();
for (let i = 0; i < mailboxes.length; i++) {
  const mb = mailboxes[i];
  let count = 0;
  try { count = mb.messages.length; } catch (e) {}
  out.push({
    name: mb.name(),
    fullName: (function () { try { return mb.fullName(); } catch (e) { return mb.name(); } })(),
    messageCount: count
  });
}

JSON.stringify(out);
