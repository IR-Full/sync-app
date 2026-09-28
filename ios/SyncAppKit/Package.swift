// swift-tools-version:5.9
import PackageDescription

// The app is a thin shell (App/) over five library targets. Keeping the layers
// as separate SPM targets is not decoration: the compiler enforces the
// dependency direction, so Presentation physically cannot reach into the wire
// protocol, and Domain cannot depend on anything at all.
//
// There are no external dependencies. The wire codec is hand-written against
// server/pkg/wire + server/proto/SyncApp/v1/body.proto, and persistence uses the
// SQLite that ships with the OS — so `swift build` works offline and nothing can
// drift out from under us on a dependency bump.
let package = Package(
    name: "SyncApp",
    defaultLocalization: "en",
    platforms: [.iOS(.v16)],
    products: [
        .library(name: "SyncAppCrypto", targets: ["SyncAppCrypto"]),
        .library(name: "SyncAppNetwork", targets: ["SyncAppNetwork"]),
        .library(name: "SyncAppDomain", targets: ["SyncAppDomain"]),
        .library(name: "SyncAppPersistence", targets: ["SyncAppPersistence"]),
        .library(name: "SyncAppPresentation", targets: ["SyncAppPresentation"]),
        .library(name: "SyncAppDI", targets: ["SyncAppDI"]),
    ],
    targets: [
        // X3DH, the Double Ratchet, safety numbers, trust pinning.
        //
        // This directory existed for a long time without being declared here, which
        // means nothing ever compiled it: SPM ignores sources outside every target
        // path silently, so `swift build` was green and the E2E implementation was
        // dead code. It has no dependency on the wire layer on purpose — the ratchet
        // knows nothing about envelopes, and keeping it that way is what lets it be
        // tested against the Go and TypeScript ports with fixed vectors.
        .target(name: "SyncAppCrypto", path: "Sources/Crypto"),

        // Wire protocol: framing, envelope, protobuf bodies, transports, client.
        .target(
            name: "SyncAppNetwork",
            dependencies: ["SyncAppCrypto"],
            path: "Sources/Network"
        ),

        // Entities + use cases. Depends on nothing — the innermost circle.
        .target(name: "SyncAppDomain", path: "Sources/Domain"),

        // SQLite cache, outbox, and the repository implementations that join the
        // network client to the cache (offline-first lives here).
        .target(
            name: "SyncAppPersistence",
            dependencies: ["SyncAppDomain", "SyncAppNetwork", "SyncAppCrypto"],
            path: "Sources/Persistence",
            linkerSettings: [.linkedLibrary("sqlite3")]
        ),

        // SwiftUI screens + ViewModels. Talks to Domain protocols only.
        .target(
            name: "SyncAppPresentation",
            dependencies: ["SyncAppDomain"],
            path: "Sources/Presentation"
        ),

        // Composition root: the only place that knows every concrete type.
        .target(
            name: "SyncAppDI",
            dependencies: ["SyncAppNetwork", "SyncAppDomain", "SyncAppPersistence", "SyncAppPresentation"],
            path: "Sources/DI"
        ),

        .testTarget(name: "SyncAppCryptoTests", dependencies: ["SyncAppCrypto"], path: "Tests/CryptoTests"),
        .testTarget(name: "SyncAppNetworkTests", dependencies: ["SyncAppNetwork"], path: "Tests/NetworkTests"),
        .testTarget(name: "SyncAppDomainTests", dependencies: ["SyncAppDomain"], path: "Tests/DomainTests"),
        .testTarget(
            name: "SyncAppPersistenceTests",
            dependencies: ["SyncAppPersistence", "SyncAppDomain", "SyncAppNetwork", "SyncAppCrypto"],
            path: "Tests/PersistenceTests"
        ),

        // Depends on SyncAppDI, which transitively pulls in every other target.
        // That is the point as much as the assertions are: without it nothing
        // compiles the SwiftUI layer until the app target does, and a failure
        // there surfaces as `no such module` against the app's first import —
        // pointing nowhere near the code that actually failed to build.
        .testTarget(
            name: "SyncAppPresentationTests",
            dependencies: ["SyncAppPresentation", "SyncAppDI", "SyncAppDomain", "SyncAppNetwork"],
            path: "Tests/PresentationTests"
        ),
    ]
)
