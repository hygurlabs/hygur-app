ObjC.import('Foundation');

const Mail = Application("Mail");
const result = { running: false, accountCount: 0, mailboxCount: 0 };

try {
  result.running = Mail.running();
  if (result.running) {
    result.accountCount = Mail.accounts.length;
    result.mailboxCount = Mail.mailboxes.length;
  }
} catch (e) {
  result.error = String(e);
}

JSON.stringify(result);
