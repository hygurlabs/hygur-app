ObjC.import('Foundation');

function getArgs() {
  const env = $.NSProcessInfo.processInfo.environment;
  const raw = ObjC.unwrap(env.objectForKey("HYGUR_JXA_ARGS")) || "{}";
  return JSON.parse(raw);
}

function stripPrefix(s) {
  if (!s) return "";
  return String(s).replace(/^\s*(re|fwd|fw|tr|tr\.|rep)\s*:\s*/i, '').trim();
}

function findMailbox(Mail, accountId, mailboxName) {
  if (accountId) {
    const accts = Mail.accounts();
    for (let i = 0; i < accts.length; i++) {
      if (accts[i].id() === accountId) {
        const mbs = accts[i].mailboxes();
        for (let j = 0; j < mbs.length; j++) {
          if (mbs[j].name() === mailboxName) return mbs[j];
        }
        // Fall through: account exists but no matching mailbox under it
      }
    }
  }
  // Try local mailboxes (Drafts, Outbox, Importation, etc.)
  const local = Mail.mailboxes();
  for (let i = 0; i < local.length; i++) {
    if (local[i].name() === mailboxName) return local[i];
  }
  return null;
}

const args = getArgs();
// args: { accountId, mailboxName, since?: ISO string, before?: ISO string, limit?: number, offset?: number }

const Mail = Application("Mail");
const mb = findMailbox(Mail, args.accountId || "", args.mailboxName);
if (!mb) throw new Error("mailbox not found: " + (args.accountId || "(local)") + "/" + args.mailboxName);

// Bulk-fetch all attributes in a few Apple Events instead of N×4.
const ids = mb.messages.id();
const subjects = mb.messages.subject();
// Order/group by RECEIVED date so a just-received mail is treated as the
// newest even when it was SENT earlier (a reply to an old thread, a delayed
// delivery). dateSent alone buried/mis-grouped freshly-received mail. Fall
// back to sent date per-message when received is unavailable.
const sentDates = mb.messages.dateSent();
let recvDates = [];
try { recvDates = mb.messages.dateReceived(); } catch (e) { recvDates = []; }
const senders = mb.messages.sender();
let messageIds = [];
try { messageIds = mb.messages.messageId(); } catch (e) { messageIds = []; }

const since = args.since ? new Date(args.since) : null;
const before = args.before ? new Date(args.before) : null;

const groups = new Map();
for (let i = 0; i < ids.length; i++) {
  const d = recvDates[i] || sentDates[i];
  if (since && d < since) continue;
  if (before && d >= before) continue;

  const subjRaw = subjects[i] || "";
  const key = stripPrefix(subjRaw).toLowerCase() || ("__solo__" + ids[i]);
  let g = groups.get(key);
  if (!g) {
    g = {
      id: messageIds[i] || ("local-" + ids[i]),
      subject: subjRaw,
      participants: new Set(),
      ids: [],
      dateMin: d,
      dateMax: d
    };
    groups.set(key, g);
  }
  if (senders[i]) g.participants.add(String(senders[i]));
  g.ids.push(ids[i]);
  // Key the thread on its OLDEST message (the root) so the content_id is
  // deterministic across syncs — Apple Mail returns messages in mailbox/storage
  // order, so "first seen" was non-deterministic and could spawn duplicate
  // thread items. get_messages fetches by the numeric id list, not this id.
  if (d < g.dateMin) {
    g.dateMin = d;
    g.id = messageIds[i] || ("local-" + ids[i]);
  }
  if (d > g.dateMax) g.dateMax = d;
}

const out = [];
for (const g of groups.values()) {
  out.push({
    id: g.id,
    subject: g.subject,
    participants: Array.from(g.participants),
    dateStart: g.dateMin.toISOString(),
    dateEnd: g.dateMax.toISOString(),
    messageCount: g.ids.length,
    messageIds: g.ids,
    mailbox: args.mailboxName
  });
}

out.sort(function (a, b) { return b.dateEnd.localeCompare(a.dateEnd); });

const offset = Math.max(0, args.offset || 0);
const limit = args.limit && args.limit > 0 ? args.limit : out.length;

JSON.stringify(out.slice(offset, offset + limit));
