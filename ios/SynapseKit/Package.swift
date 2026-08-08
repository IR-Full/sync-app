// swift-tools-version:5.9
import PackageDescription

// The app is a thin shell (App/) over five library targets. Keeping the layers
// as separate SPM targets is not decoration: the compiler enforces the
// dependency direction, so Presentation physically cannot reach into the wire
// protocol, and Domain cannot depend on anything at all.
//
// There are no external dependencies. The wire codec is hand-written against
// server/pkg/wire + server/proto/synapse/v1/body.proto, and persistence uses the
// SQLite that ships with the OS — so `swift build` works offline and nothing can
// drift out from under us on a dependency bump.
let package = Package(
    name: "Synapse",
    defaultLocalization: "en",
    platforms: [.iOS(.v16)],
    products: [
        .library(name: "SynapseNetwork", targets: ["SynapseNetwork"]),
        .library(name: "SynapseDomain", targets: ["SynapseDomain"]),
        .library(name: "SynapsePersistence", targets: ["SynapsePersistence"]),
        .library(name: "SynapsePresentation", targets: ["SynapsePresentation"]),
        .library(name: "SynapseDI", targets: ["SynapseDI"]),
    ],
    targets: [
        // Wire protocol: framing, envelope, protobuf bodies, transports, client.
        .target(name: "SynapseNetwork", path: "Sources/Network"),

        // Entities + use cases. Depends on nothing — the innermost circle.
        .target(name: "SynapseDomain", path: "Sources/Domain"),

        // SQLite cache, outbox, and the repository implementations that join the
        // network client to the cache (offline-first lives here).
        .target(
            name: "SynapsePersistence",
            dependencies: ["SynapseDomain", "SynapseNetwork"],
            path: "Sources/Persistence",
            linkerSettings: [.linkedLibrary("sqlite3")]
        ),

        // SwiftUI screens + ViewModels. Talks to Domain protocols only.
        .target(
            name: "SynapsePresentation",
            dependencies: ["SynapseDomain"],
            path: "Sources/Presentation"
        ),

        // Composition root: the only place that knows every concrete type.
        .target(
            name: "SynapseDI",
            dependencies: ["SynapseNetwork", "SynapseDomain", "SynapsePersistence", "SynapsePresentation"],
            path: "Sources/DI"
        ),

        .testTarget(name: "SynapseNetworkTests", dependencies: ["SynapseNetwork"], path: "Tests/NetworkTests"),
        .testTarget(name: "SynapseDomainTests", dependencies: ["SynapseDomain"], path: "Tests/DomainTests"),
        .testTarget(
            name: "SynapsePersistenceTests",
            dependencies: ["SynapsePersistence", "SynapseDomain", "SynapseNetwork"],
            path: "Tests/PersistenceTests"
        ),

        // Depends on SynapseDI, which transitively pulls in every other target.
        // That is the point as much as the assertions are: without it nothing
        // compiles the SwiftUI layer until the app target does, and a failure
        // there surfaces as `no such module` against the app's first import —
        // pointing nowhere near the code that actually failed to build.
        .testTarget(
            name: "SynapsePresentationTests",
            dependencies: ["SynapsePresentation", "SynapseDI", "SynapseDomain", "SynapseNetwork"],
            path: "Tests/PresentationTests"
        ),
    ]
)
