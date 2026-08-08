import QuickLook
import SwiftUI
import SynapseDomain
import UIKit

/// One message bubble.
///
/// The delivery tick is the visible face of the protocol's three-step lifecycle:
/// queued locally (`sending`), durably persisted with a `chat_seq` (`sent`), and
/// read by the other side (`read`). A message that never got past the first step
/// gets a retry affordance rather than silently sitting there — the outbox will
/// try again on reconnect, but the user should be able to tell.
struct MessageRow: View {
    let message: Message
    let isOutgoing: Bool
    let senderName: String
    let showSender: Bool
    let canEdit: Bool
    /// Set once the attachment's bytes are on disk.
    let mediaURL: URL?
    let onLoadMedia: () -> Void
    let onRetry: () -> Void
    let onEdit: () -> Void
    let onDelete: () -> Void
    let onReact: (String) -> Void
    let onReply: () -> Void

    private static let quickReactions = ["👍", "❤️", "😂", "🔥", "🎉"]

    var body: some View {
        HStack {
            if isOutgoing { Spacer(minLength: 48) }

            VStack(alignment: isOutgoing ? .trailing : .leading, spacing: 4) {
                if showSender && !isOutgoing {
                    Text(senderName)
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(Theme.avatarColor(for: message.senderID))
                }

                bubble

                if !message.reactions.isEmpty {
                    reactionChips
                }
            }

            if !isOutgoing { Spacer(minLength: 48) }
        }
        .contextMenu {
            Button { onReply() } label: { Label(l("chat.reply"), systemImage: "arrowshape.turn.up.left") }
            if canEdit {
                Button { onEdit() } label: { Label(l("chat.edit"), systemImage: "pencil") }
            }
            ForEach(Self.quickReactions, id: \.self) { emoji in
                Button { onReact(emoji) } label: { Text(emoji) }
            }
            if !message.isDeleted {
                Button(role: .destructive) { onDelete() } label: {
                    Label(l("chat.delete"), systemImage: "trash")
                }
            }
        }
    }

    @ViewBuilder
    private var bubble: some View {
        VStack(alignment: .leading, spacing: 4) {
            if let forward = message.forwardedFrom {
                Text(l("chat.forwarded", forward.senderID))
                    .font(.caption2.italic())
                    .foregroundStyle(isOutgoing ? Theme.outgoingText.opacity(0.8) : .secondary)
            }

            if message.isDeleted {
                Text(l("chat.deleted"))
                    .italic()
                    .foregroundStyle(.secondary)
            } else {
                if let attachment = message.attachment {
                    AttachmentView(
                        attachment: attachment,
                        localURL: mediaURL,
                        isOutgoing: isOutgoing,
                        onLoad: onLoadMedia
                    )
                }
                if !message.text.isEmpty {
                    Text(message.text)
                        .foregroundStyle(isOutgoing ? Theme.outgoingText : Theme.incomingText)
                        .textSelection(.enabled)
                }
            }

            HStack(spacing: 4) {
                if message.isEdited && !message.isDeleted {
                    Text(l("chat.edited"))
                        .font(.caption2)
                }
                if message.expiresAt != nil {
                    Image(systemName: "flame")
                        .font(.caption2)
                }
                Text(message.sentAt.messageStamp())
                    .font(.caption2)
                if isOutgoing {
                    deliveryIndicator
                }
            }
            .foregroundStyle(isOutgoing ? Theme.outgoingText.opacity(0.75) : .secondary)
            .frame(maxWidth: .infinity, alignment: .trailing)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .background(
            RoundedRectangle(cornerRadius: 16, style: .continuous)
                .fill(isOutgoing ? Theme.outgoingBubble : Theme.incomingBubble)
        )
    }

    @ViewBuilder
    private var deliveryIndicator: some View {
        switch message.state {
        case .sending:
            Image(systemName: "clock").font(.caption2)
        case .sent:
            Image(systemName: "checkmark").font(.caption2)
        case .read:
            Image(systemName: "checkmark.circle.fill").font(.caption2)
        case .failed:
            Button(action: onRetry) {
                Image(systemName: "exclamationmark.arrow.circlepath")
                    .font(.caption2)
                    .foregroundStyle(.red)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(l("chat.retry"))
        }
    }

    private var reactionChips: some View {
        HStack(spacing: 4) {
            ForEach(message.reactions.sorted(by: { $0.key < $1.key }), id: \.key) { emoji, count in
                Button { onReact(emoji) } label: {
                    Text(count > 1 ? "\(emoji) \(count)" : emoji)
                        .font(.caption2)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 3)
                        .background(Capsule().fill(Color(uiColor: .tertiarySystemFill)))
                }
                .buttonStyle(.plain)
            }
        }
    }
}

/// An attachment: a thumbnail once the bytes are local, a tappable card before
/// that.
///
/// Nothing downloads on appear. A `media_ref` needs a signed-URL round trip
/// before the blob can be fetched, so auto-loading every row while scrolling
/// would spend the connection on files nobody opened. The metadata travels with
/// the message precisely so the card can be drawn without the bytes.
private struct AttachmentView: View {
    let attachment: Attachment
    let localURL: URL?
    let isOutgoing: Bool
    let onLoad: () -> Void

    @State private var isPresentingFile = false
    @State private var image: UIImage?

    var body: some View {
        Group {
            if attachment.kind == .image, let image {
                Image(uiImage: image)
                    .resizable()
                    .scaledToFill()
                    .frame(maxWidth: 240, maxHeight: 300)
                    .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
            } else if localURL != nil {
                card(icon: icon, subtitle: l("attachment.open"))
                    .onTapGesture { isPresentingFile = true }
            } else {
                Button(action: onLoad) {
                    card(icon: "arrow.down.circle", subtitle: sizeLabel)
                }
                .buttonStyle(.plain)
            }
        }
        .quickLookPreview(previewURL)
        // Decoding happens off the main actor and only when the file actually
        // appears — a computed property here would re-read and re-decode the
        // image on every layout pass of a scrolling list.
        .task(id: localURL) { await decodeImageIfNeeded() }
    }

    private func decodeImageIfNeeded() async {
        guard attachment.kind == .image, let localURL, image == nil else { return }
        let decoded = await Task.detached(priority: .userInitiated) {
            (try? Data(contentsOf: localURL)).flatMap(UIImage.init(data:))
        }.value
        image = decoded
    }

    /// `quickLookPreview` wants a binding; the sheet state and the URL are the
    /// same fact, so they are derived from one another rather than duplicated.
    private var previewURL: Binding<URL?> {
        Binding(
            get: { isPresentingFile ? localURL : nil },
            set: { if $0 == nil { isPresentingFile = false } }
        )
    }

    private func card(icon: String, subtitle: String) -> some View {
        HStack(spacing: 8) {
            Image(systemName: icon)
                .font(.title3)
            VStack(alignment: .leading, spacing: 2) {
                Text(attachment.filename.isEmpty ? label : attachment.filename)
                    .font(.footnote.weight(.medium))
                    .lineLimit(1)
                Text(subtitle)
                    .font(.caption2)
                    .opacity(0.8)
            }
        }
        .foregroundStyle(isOutgoing ? Theme.outgoingText : Theme.incomingText)
        .padding(.vertical, 4)
    }

    private var sizeLabel: String {
        attachment.size > 0
            ? ByteCountFormatter.string(fromByteCount: attachment.size, countStyle: .file)
            : l("attachment.tap.to.load")
    }

    private var icon: String {
        switch attachment.kind {
        case .image: return "photo"
        case .video, .videoNote: return "video"
        case .voice: return "waveform"
        case .file, .unknown: return "doc"
        }
    }

    private var label: String {
        switch attachment.kind {
        case .image: return l("attachment.image")
        case .video: return l("attachment.video")
        case .videoNote: return l("attachment.video.note")
        case .voice: return l("attachment.voice")
        case .file, .unknown: return l("attachment.file")
        }
    }
}
