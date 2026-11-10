// Each top-level exported function is a command. The host passes the current
// outgoing message and exposes Telegram actions through the `host` global.
export function re(update) {
  const chatID = update.chat_id;
  const messageID = update.message_id;
  if (!update.reply_to) {
    host.editMessage(chatID, messageID, "Usage: re <count> reply to a message to repeat it.");
    return;
  }

  let count = 1;
  if (update.args.length > 0) {
    const arg = update.args[0];
    count = Number(arg);
    if (!/^[+-]?\d+$/.test(arg) || count < 1 || count > 100) {
      host.editMessage(chatID, messageID, "Invalid count, must be between 1 and 100");
      return;
    }
  }

  const errors = [];
  try {
    host.deleteMessages(chatID, [messageID]);
  } catch (err) {
    errors.push(err);
  }
  for (let i = 0; i < count; i++) {
    try {
      host.forwardMessages(chatID, chatID, [update.reply_to.id]);
    } catch (err) {
      errors.push(err);
    }
  }

  if (errors.length === 0) {
    return;
  }
  const first = errors[0];
  if (errors.every((err) => err && err.message === first.message)) {
    throw first;
  }
  throw new Error(errors.map((err) => (err && err.message) || String(err)).join("; "));
}
