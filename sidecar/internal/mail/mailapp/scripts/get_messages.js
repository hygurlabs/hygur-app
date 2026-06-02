ObjC.import('Foundation');

function getArgs() {
  const env = $.NSProcessInfo.processInfo.environment;
  const raw = ObjC.unwrap(env.objectForKey("HYGUR_JXA_ARGS")) || "{}";
  return JSON.parse(raw);
}

function findMailbox(Mail, accountId, mailboxName) {
  if (accountId && mailboxName) {
    const accts = Mail.accounts();
    for (let i = 0; i < accts.length; i++) {
      if (accts[i].id() === accountId) {
        const mbs = accts[i].mailboxes();
        for (let j = 0; j < mbs.length; j++) {
          if (mbs[j].name() === mailboxName) return mbs[j];
        }
      }
    }
  }
  if (mailboxName) {
    const local = Mail.mailboxes();
    for (let i = 0; i < local.length; i++) {
      if (local[i].name() === mailboxName) return local[i];
    }
  }
  return null;
}

function safe(fn, def) {
  try { return fn(); } catch (e) { return def; }
}

const args = getArgs();
// args: { ids: number[], accountId?: string, mailboxName?: string }

const Mail = Application("Mail");

// Resolve a single source mailbox (preferred) or fall back to inbox.
let source = findMailbox(Mail, args.accountId || "", args.mailboxName || "");
if (!source) source = Mail.inbox; // unified inbox covers most "received" cases

const out = [];

for (let k = 0; k < args.ids.length; k++) {
  const targetId = args.ids[k];
  let m = null;
  try {
    m = source.messages.byId(targetId);
    void m.id();
  } catch (e) {
    // Fallback: scan all accounts' mailboxes once for the missing id.
    try {
      const accts = Mail.accounts();
      outer: for (let i = 0; i < accts.length; i++) {
        const mbs = accts[i].mailboxes();
        for (let j = 0; j < mbs.length; j++) {
          try {
            const candidate = mbs[j].messages.byId(targetId);
            void candidate.id();
            m = candidate;
            break outer;
          } catch (e2) {}
        }
      }
    } catch (e3) {}
  }

  if (!m) {
    out.push({ id: targetId, error: "not_found" });
    continue;
  }

  const attachments = [];
  try {
    const atts = m.mailAttachments();
    for (let j = 0; j < atts.length; j++) {
      const a = atts[j];
      attachments.push({
        filename: safe(function () { return a.name(); }, ""),
        mimeType: safe(function () { return a.mimeType(); }, ""),
        size: safe(function () { return a.fileSize(); }, 0)
      });
    }
  } catch (e) {}

  out.push({
    id: m.id(),
    msgId: safe(function () { return m.messageId(); }, ""),
    subject: safe(function () { return m.subject(); }, ""),
    from: safe(function () { return m.sender(); }, ""),
    replyTo: safe(function () { return m.replyTo(); }, ""),
    date: safe(function () { return (m.dateReceived() || m.dateSent()).toISOString(); }, ""),
    body: safe(function () { return m.content(); }, ""),
    source: safe(function () { return m.source(); }, ""),
    attachments: attachments,
    accountId: safe(function () { return m.mailbox.account.id(); }, ""),
    mailbox: safe(function () { return m.mailbox.name(); }, "")
  });
}

JSON.stringify(out);
