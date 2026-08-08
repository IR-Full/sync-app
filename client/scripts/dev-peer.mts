/**
 * A second user that stays connected and auto-replies.
 *
 * Useful while developing the web client: it gives you a real counterpart on the
 * gateway to exchange messages with, so live fanout, typing indicators and read
 * receipts can be exercised without opening a second browser profile.
 *
 *   node --experimental-strip-types --import ./scripts/ts-resolve.mjs scripts/dev-peer.mts [username]
 */
import { SynapseClient } from '../src/shared/api/protocol/client.ts'
import { MsgType } from '../src/shared/api/protocol/msg-type.ts'

const url = process.env.SYNAPSE_WS_URL ?? 'ws://localhost:8080/ws'
const username = process.argv[2] ?? 'webbob'
const password = 'correct-horse-battery'

const client = new SynapseClient({ url })
client.setDeviceId(`dev-peer-${username}`)

async function main() {
  let session
  try {
    session = await client.connect({ kind: 'password', username, password, register: true })
    console.log(`registered ${username} (${session.userId})`)
  } catch {
    session = await client.connect({ kind: 'password', username, password, register: false })
    console.log(`logged in ${username} (${session.userId})`)
  }

  client.on('message', (message) => {
    if (message.senderId === session.userId) return
    console.log(`<- ${message.chatId} #${message.chatSeq}: ${message.text}`)

    // Mark read so the sender sees the double tick, then reply.
    client.send(MsgType.READ, { chatId: message.chatId, upToChatSeq: message.chatSeq })
    client.send(MsgType.TYPING, { chatId: message.chatId, active: true })

    setTimeout(() => {
      void client
        .request(
          MsgType.SEND,
          {
            chatId: message.chatId,
            dedupKey: `peer-${message.messageId}`,
            text: `эхо: ${message.text}`,
          },
          { expect: MsgType.SEND_ACK },
        )
        .then(() => console.log('-> replied'))
        .catch((error) => console.error('reply failed', error))
    }, 1200)
  })

  console.log('listening… (ctrl-c to stop)')
}

main().catch((error) => {
  console.error(error)
  process.exit(1)
})
