import AppKit
import UserNotifications
import Security

// The provider key belongs to the Go sender. This app only registers a token
// and records receipts, using the entitlement embedded by its signing profile.
final class LabDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    private var window: NSWindow!
    private let status = NSTextView()
    private let directory: URL
    private var environment = ""

    override init() {
        let args = CommandLine.arguments
        let path = args.count == 3 && args[1] == "--output" ? args[2] : ""
        directory = URL(fileURLWithPath: path, isDirectory: true)
        super.init()
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 720, height: 360),
                          styleMask: [.titled, .closable, .miniaturizable, .resizable],
                          backing: .buffered, defer: false)
        window.title = "DeviceWeave Push Lab"
        status.isEditable = false
        status.font = .monospacedSystemFont(ofSize: 13, weight: .regular)
        let scroll = NSScrollView(frame: window.contentView!.bounds)
        scroll.autoresizingMask = [.width, .height]
        scroll.hasVerticalScroller = true
        scroll.documentView = status
        status.frame = scroll.bounds
        status.autoresizingMask = [.width, .height]
        window.contentView!.addSubview(scroll)
        window.center()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        guard CommandLine.arguments.count == 3, CommandLine.arguments[1] == "--output" else {
            show("Launch with --output followed by the absolute gitignored lab app directory.")
            return
        }
        do {
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true,
                                                    attributes: [.posixPermissions: 0o700])
            environment = try signingEnvironment()
        } catch { show("Cannot register: \(error.localizedDescription)"); return }
        let center = UNUserNotificationCenter.current()
        center.delegate = self
        center.requestAuthorization(options: [.alert, .badge, .sound]) { granted, error in
            DispatchQueue.main.async {
                self.show("Notification display permission: \(granted ? "granted" : "not granted")")
                if let error = error { self.show(error.localizedDescription) }
                NSApp.registerForRemoteNotifications()
            }
        }
        show("Registering \(Bundle.main.bundleIdentifier ?? "unknown") in \(environment)…")
    }

    func application(_ application: NSApplication, didRegisterForRemoteNotificationsWithDeviceToken token: Data) {
        do {
            try save(["token": token.map { String(format: "%02x", $0) }.joined(),
                      "topic": Bundle.main.bundleIdentifier!, "environment": environment,
                      "registeredAt": ISO8601DateFormatter().string(from: Date())], as: "registration.json")
            show("Token saved to registration.json. Ready for a remote alert or background test.")
        } catch { show("Token export failed: \(error.localizedDescription)") }
    }

    func application(_ application: NSApplication, didFailToRegisterForRemoteNotificationsWithError error: Error) {
        show("APNs registration failed: \(error.localizedDescription)")
    }

    func application(_ application: NSApplication, didReceiveRemoteNotification userInfo: [String: Any]) {
        receipt(userInfo, source: "application")
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                withCompletionHandler completion: @escaping (UNNotificationPresentationOptions) -> Void) {
        receipt(notification.request.content.userInfo, source: "foreground")
        completion([.banner, .sound])
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                withCompletionHandler completion: @escaping () -> Void) {
        receipt(response.notification.request.content.userInfo, source: "interaction")
        completion()
    }

    private func receipt(_ payload: [AnyHashable: Any], source: String) {
        DispatchQueue.main.async {
            let correlation = payload["labCorrelationID"] as? String ?? "missing"
            do {
                try self.save(["receivedAt": ISO8601DateFormatter().string(from: Date()),
                               "labCorrelationID": correlation, "source": source,
                               "environment": self.environment,
                               "topic": Bundle.main.bundleIdentifier!,
                               "payload": payload], as: "receipt-\(UUID().uuidString).json")
                self.show("Received \(source) notification: \(correlation)")
            } catch { self.show("Receipt export failed: \(error.localizedDescription)") }
        }
    }

    private func save(_ value: [String: Any], as name: String) throws {
        let data = try JSONSerialization.data(withJSONObject: value, options: [.prettyPrinted, .sortedKeys])
        let file = directory.appendingPathComponent(name)
        try data.write(to: file, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: file.path)
    }

    private func signingEnvironment() throws -> String {
        var code: SecCode?
        var info: CFDictionary?
        var staticCode: SecStaticCode?
        guard SecCodeCopySelf([], &code) == errSecSuccess, let code = code,
              SecCodeCopyStaticCode(code, [], &staticCode) == errSecSuccess, let staticCode = staticCode,
              SecCodeCopySigningInformation(staticCode, SecCSFlags(rawValue: kSecCSSigningInformation), &info) == errSecSuccess,
              let dictionary = info as? [String: Any],
              let entitlements = dictionary[kSecCodeInfoEntitlementsDict as String] as? [String: Any],
              let env = entitlements["com.apple.developer.aps-environment"] as? String,
              ["development", "production"].contains(env) else {
            throw NSError(domain: "PushLab", code: 1, userInfo: [NSLocalizedDescriptionKey:
                "The app needs a valid macOS provisioning profile and APNs signing entitlement."])
        }
        return env
    }

    private func show(_ text: String) {
        status.string += text + "\n"
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }
}

let application = NSApplication.shared
let delegate = LabDelegate()
application.delegate = delegate
application.setActivationPolicy(.regular)
application.run()
