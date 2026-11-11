// Signals: your AI agent sessions, and what they use, in the macOS menu bar.
//
// A native menu bar app (no Dock icon). The menu bar item shows one dot per
// agent session, then (with Usage on) the gate and your usage: plan limits as
// a percentage, or API spend in dollars. The menu lists the sessions and the
// usage details; the window sets up both.
//
// It holds no logic of its own: sessions come from libexec/signals-state, usage
// from libexec/signals-meter, and every change goes through bin/signals, all
// covered by the tests.
//
//   Signals                                  run in the menu bar
//   signals://settings[?welcome=1|page=usage] open the window
//   signals://refresh                         re-read usage now
//   Signals --render DIR                      write the window pages, menu and menu bar item as PNG
//   Signals --icon DIR                        write an iconset to DIR (for the app icon)

import AppKit
import SwiftUI
import UserNotifications

// MARK: - Look

let lampRed = Color(red: 1.0, green: 0.27, blue: 0.23)
let lampAmber = Color(red: 1.0, green: 0.84, blue: 0.04)
let lampGreen = Color(red: 0.2, green: 0.84, blue: 0.29)
let amber = Color(red: 1.0, green: 0.62, blue: 0.04)
let night = LinearGradient(colors: [Color(red: 0.08, green: 0.09, blue: 0.11), Color(red: 0.14, green: 0.15, blue: 0.2)],
                           startPoint: .topLeading, endPoint: .bottomTrailing)
let windowSize = CGSize(width: 760, height: 600)

// MARK: - Engine bridge

enum Engine {
    static let libexec = Bundle.main.bundleURL.deletingLastPathComponent()
    static var cli: URL {
        if let p = ProcessInfo.processInfo.environment["SIGNALS_CLI"] { return URL(fileURLWithPath: p) }
        return libexec.deletingLastPathComponent().appendingPathComponent("bin/signals")
    }
    static var stateScript: URL { libexec.appendingPathComponent("signals-state") }
    static var meter: URL { libexec.appendingPathComponent("signals-meter") }
    static var home: URL {
        if let h = ProcessInfo.processInfo.environment["SIGNALS_HOME"] { return URL(fileURLWithPath: h) }
        return FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".signals")
    }
    static var sessionsDir: URL { home.appendingPathComponent("sessions") }

    static func run(_ exe: URL, _ args: [String] = [], env extra: [String: String] = [:]) async -> (Int32, String, String) {
        await withCheckedContinuation { done in
            DispatchQueue.global(qos: .utility).async {
                let p = Process()
                p.executableURL = exe; p.arguments = args
                var env = ProcessInfo.processInfo.environment
                // Homebrew's bin on Apple silicon and Intel, built at run time so the
                // Homebrew bottle carries no install location and works on both.
                env["PATH"] = (["opt", "homebrew"].joined(separator: "/") + "/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin").withRootSlash
                extra.forEach { env[$0.key] = $0.value }
                p.environment = env
                let out = Pipe(), err = Pipe()
                p.standardOutput = out; p.standardError = err; p.standardInput = FileHandle.nullDevice
                do { try p.run() } catch { done.resume(returning: (127, "", error.localizedDescription)); return }
                let o = String(decoding: out.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
                let e = String(decoding: err.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
                p.waitUntilExit()
                done.resume(returning: (p.terminationStatus, o, e))
            }
        }
    }

    /// `signals args...` without prompts or app launches.
    static func signals(_ args: [String], env: [String: String] = [:]) async -> (Int32, String, String) {
        await run(cli, args, env: env.merging(["SIGNALS_NO_PROMPT": "1", "SIGNALS_UI": "text", "SIGNALS_NO_APP": "1"]) { a, _ in a })
    }

    static func clean(_ s: String) -> String {
        s.replacingOccurrences(of: "\u{1B}\\[[0-9;]*m", with: "", options: .regularExpression)
            .replacingOccurrences(of: "==> ", with: "").trimmingCharacters(in: .whitespacesAndNewlines)
    }
}

// MARK: - Sessions model

struct Row: Decodable {
    let agent: String, who: String, status: String, label: String, name: String, cwd: String
    let updated: Double
    let detail: String?
    let session_id: String?
}
struct SessionState: Decodable {
    let now: Double
    let rows: [Row]
    let counts: [String: Int]
}

// MARK: - Usage model (libexec/signals-meter; every field is optional)

struct Usage: Decodable {
    struct Window: Decodable { let pct: Double?; let resets_at: Double?; let active: Bool? }
    struct Credits: Decodable { let enabled: Bool?; let used: Double?; let limit: Double?; let reason: String? }
    struct Share: Decodable { let name: String; let pct: Double }
    struct Account: Decodable {
        let session: Window?; let weekly: Window?; let locked: String?; let credits: Credits?
        let breakdown: [Share]?; let source: String?; let as_of: Double?; let eta: Double?
        let outdated: Bool?
    }
    struct Plan: Decodable { let label: String? }
    struct API: Decodable { let error: String?; let stale: Bool? }
    struct Session: Decodable { let sid: String?; let cost: Double; let cwd: String?; let title: String?; let state: String?; let times: Double? }
    struct Note: Decodable { let title: String; let body: String }
    struct Codex: Decodable { let plan: String; let session: Window?; let weekly: Window?; let status: String?; let as_of: Double? }
    let codex: Codex?
    let mode: String; let status: String?; let month: String?; let updated: Double?
    let plan: Plan?; let account: Account?; let limit_hits_week: Int?
    let spend: Double?; let today_spend: Double?; let projected: Double?; let over_at: Double?; let spike: Double?
    let budget: Double?; let pct: Double?; let pass_until: Double?; let live_since: Double?
    let sessions: [Session]?; let api: API?; let notify: Note?

    var on: Bool { ["plan", "anthropic", "openai", "estimate"].contains(mode) }
    /// Money per session only where money is what's counted (never on a plan).
    var costsSessions: Bool { mode == "anthropic" || mode == "estimate" }
}

func money(_ v: Double) -> String { String(format: "$%.2f", v) }
func shortMoney(_ v: Double) -> String { v >= 100 ? "$\(Int(v.rounded()))" : money(v) }
func clock(_ t: Double) -> String {
    let f = DateFormatter(); f.dateFormat = "h:mma"; f.amSymbol = "am"; f.pmSymbol = "pm"
    return f.string(from: Date(timeIntervalSince1970: t))
}
func dayClock(_ t: Double) -> String {
    let f = DateFormatter(); f.dateFormat = "EEE ha"; f.amSymbol = "am"; f.pmSymbol = "pm"
    return f.string(from: Date(timeIntervalSince1970: t))
}
func ago(_ t: Double) -> String {
    let s = max(0, Int(Date().timeIntervalSince1970 - t))
    return s < 90 ? "just now" : s < 3600 ? "\(s / 60)m ago" : s < 86400 ? "\(s / 3600)h ago" : "\(s / 86400)d ago"
}
func shortDate(_ t: Double) -> String { let f = DateFormatter(); f.dateFormat = "MMM d"; return f.string(from: Date(timeIntervalSince1970: t)) }
func level(_ pct: Double) -> Color { pct >= 100 ? lampRed : pct >= 80 ? amber : lampGreen }

// MARK: - The menu bar item: session dots, the gate, and your usage

func lampColor(_ status: String) -> NSColor {
    switch status {
    case "working": return NSColor(red: 1.0, green: 0.27, blue: 0.23, alpha: 1)
    case "waiting": return NSColor(red: 1.0, green: 0.78, blue: 0.0, alpha: 1)
    case "error": return NSColor(white: 0.45, alpha: 1)
    default: return NSColor(red: 0.2, green: 0.8, blue: 0.29, alpha: 1)
    }
}

enum StatusArt {
    static let maxDots = 5

    /// What the gate looks like now: nil when Usage is off.
    static func gateColor(_ u: Usage?, fg: NSColor) -> NSColor? {
        guard let u, u.on else { return nil }
        let main: NSColor
        switch u.status {
        case "warn": main = .systemOrange
        case "blocked": main = .systemRed
        case "credits": main = .systemBlue
        case "over": main = u.pass_until != nil ? .systemOrange : .systemRed
        default: main = fg
        }
        // Codex near or at its limit shows on the same gate.
        switch u.codex?.status {
        case "blocked": return .systemRed
        case "warn": return main == fg ? .systemOrange : main
        default: return main
        }
    }

    /// The plan limit to show: the 5-hour window by default, or the week once
    /// it's nearly used up (80%+) and further along than the 5-hour window.
    static func closest(session: Usage.Window?, weekly: Usage.Window?) -> (label: String, pct: Double)? {
        let w = weekly?.pct
        guard let session else { return w.map { ("wk", $0) } }
        let s = (session.active ?? false) ? session.pct ?? 0 : 0   // no window running yet: 0%
        if let w, w >= 80, w > s { return ("wk", w) }
        return ("5h", s)
    }

    /// The figure next to the gate: on a plan, the percentage of the 5-hour
    /// window (or of the week once it's nearly used up), for whichever agent is
    /// further along (money only on usage credits); on an API key or estimate,
    /// dollars.
    static func figure(_ u: Usage?) -> (text: String, agent: String?) {
        guard let u, u.on else { return ("", nil) }
        if u.mode != "plan" { return (u.spend.map(shortMoney) ?? "?", nil) }
        if u.status == "credits" { return (money(u.account?.credits?.used ?? 0), nil) }
        var limits: [(agent: String, label: String, pct: Double)] = []
        // A Claude reading from before its limits reset says nothing about now.
        let claudeOutdated = u.account?.outdated == true && u.account?.session?.active != true
        if let a = u.account, !claudeOutdated, let x = closest(session: a.session, weekly: a.weekly) { limits.append(("claude", x.label, x.pct)) }
        if let c = u.codex, let x = closest(session: c.session, weekly: c.weekly) { limits.append(("codex", x.label, x.pct)) }
        guard let top = limits.max(by: { $0.pct < $1.pct }) else { return (claudeOutdated ? "?" : "", nil) }
        // Just the percentage: the hover text and the menu say which limit and whose.
        return ("\(Int(min(top.pct, 100)))%", nil)
    }
    static func title(_ u: Usage?) -> String { figure(u).text }

    /// The figure as the menu bar shows it: the agent's icon when needed, then the text.
    static func attributedTitle(_ u: Usage?, fg: NSColor) -> NSAttributedString {
        let f = figure(u), out = NSMutableAttributedString()
        if let agent = f.agent,
           let img = NSImage(systemSymbolName: agent == "claude" ? "sparkle" : "terminal", accessibilityDescription: agent)?
               .withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: 10.5, weight: .semibold).applying(.init(paletteColors: [fg]))) {
            let att = NSTextAttachment(); att.image = img
            att.bounds = NSRect(x: 0, y: -0.5, width: img.size.width, height: img.size.height)
            out.append(NSAttributedString(string: " "))
            out.append(NSAttributedString(attachment: att))
        }
        if !f.text.isEmpty {
            out.append(NSAttributedString(string: " " + f.text,
                                          attributes: [.font: NSFont.monospacedDigitSystemFont(ofSize: 13, weight: .regular), .foregroundColor: fg]))
        }
        return out
    }

    /// The gate: a post and a striped barrier arm, 16pt tall.
    static func drawGate(at x: CGFloat, color: NSColor) {
        color.set()
        NSBezierPath(roundedRect: NSRect(x: x + 1, y: 2, width: 3.5, height: 11), xRadius: 1, yRadius: 1).fill()
        let arm = NSBezierPath(roundedRect: NSRect(x: x + 4, y: 10, width: 11.5, height: 3.2), xRadius: 1.6, yRadius: 1.6)
        arm.lineWidth = 1.2; arm.stroke()
        for sx in [6.5, 10.5] { NSBezierPath(rect: NSRect(x: x + sx, y: 10, width: 2, height: 3.2)).fill() }
    }

    static func drawIdleLight(at x: CGFloat, color: NSColor) {
        color.setStroke()
        let body = NSBezierPath(roundedRect: NSRect(x: x + 0.75, y: 0.75, width: 7.5, height: 16.5), xRadius: 2.5, yRadius: 2.5)
        body.lineWidth = 1.3; body.stroke()
        color.setFill()
        for y in [3.0, 7.75, 12.5] { NSBezierPath(ovalIn: NSRect(x: x + 2.5, y: y, width: 4, height: 2.75)).fill() }
    }

    /// Dots (or a small signal outline when there are no sessions), then the
    /// gate when Usage is on. `fg` is the menu bar's text colour.
    static func image(rows: [Row], usage: Usage?, fg: NSColor) -> NSImage {
        let d: CGFloat = 9, gap: CGFloat = 4, groupGap: CGFloat = 7
        let shown = Array(rows.prefix(maxDots)), extra = rows.count - shown.count
        let more = extra > 0 ? NSAttributedString(string: "+\(extra)", attributes: [.font: NSFont.systemFont(ofSize: 11, weight: .medium), .foregroundColor: fg]) : nil
        var width: CGFloat = rows.isEmpty ? 9 : CGFloat(shown.count) * d + CGFloat(shown.count - 1) * gap
        if let more { width += 3 + more.size().width }
        let gate = gateColor(usage, fg: fg)
        if gate != nil { width += groupGap + 16 }
        let img = NSImage(size: NSSize(width: ceil(width), height: 18), flipped: false) { _ in
            var x: CGFloat = 0
            if rows.isEmpty { drawIdleLight(at: 0, color: fg); x = 9 }
            for (i, r) in shown.enumerated() {
                lampColor(r.status).setFill()
                NSBezierPath(ovalIn: NSRect(x: CGFloat(i) * (d + gap), y: (18 - d) / 2, width: d, height: d)).fill()
                x = CGFloat(i) * (d + gap) + d
            }
            if let more { more.draw(at: NSPoint(x: x + 3, y: (18 - more.size().height) / 2)); x += 3 + more.size().width }
            if let gate { drawGate(at: x + groupGap, color: gate) }
            return true
        }
        img.isTemplate = false
        return img
    }

    /// The usage item's image: the gate, coloured by status; nil when Usage is off.
    static func gateImage(_ u: Usage?, fg: NSColor) -> NSImage? {
        guard let c = gateColor(u, fg: fg) else { return nil }
        let img = NSImage(size: NSSize(width: 16, height: 18), flipped: false) { _ in drawGate(at: 0, color: c); return true }
        img.isTemplate = false
        return img
    }

    static func dot(_ status: String) -> NSImage {
        NSImage(size: NSSize(width: 12, height: 12), flipped: false) { _ in
            lampColor(status).setFill()
            NSBezierPath(ovalIn: NSRect(x: 1.5, y: 1.5, width: 9, height: 9)).fill()
            return true
        }
    }

    /// The gate on its own, for the window's "reading your menu bar" page.
    static func gate(_ color: NSColor) -> NSImage {
        NSImage(size: NSSize(width: 18, height: 16), flipped: false) { _ in drawGate(at: 0, color: color); return true }
    }
}

// MARK: - Usage panel (in the menu)

/// One meter on one line: label, a thin bar, the figure, and when it resets.
struct MeterRow: View {
    let label: String, pct: Double?, value: String, note: String   // no pct: no bar
    var body: some View {
        HStack(spacing: 8) {
            Text(label).font(.system(size: 12)).frame(width: 52, alignment: .leading)
            GeometryReader { g in
                if let pct {
                    ZStack(alignment: .leading) {
                        Capsule().fill(Color.primary.opacity(0.1))
                        Capsule().fill(level(pct)).frame(width: max(4, g.size.width * min(pct, 100) / 100))
                    }
                }
            }
            .frame(height: 5)
            Text(value).font(.system(size: 12, weight: .semibold).monospacedDigit()).frame(width: 44, alignment: .trailing)
            Text(note).font(.system(size: 11).monospacedDigit()).foregroundStyle(.secondary).frame(width: 60, alignment: .leading)
                .lineLimit(1)
        }
        .frame(height: 16)
    }
}

/// "Agent · Plan" on the left, how old the reading is on the right.
struct PanelHeader: View {
    let title: String
    var age: Double? = nil
    var body: some View {
        HStack {
            Text(title).font(.system(size: 11.5, weight: .semibold)).foregroundStyle(.secondary).lineLimit(1)
            Spacer(minLength: 8)
            if let age { Text(ago(age)).font(.system(size: 10.5)).foregroundStyle(.tertiary) }
        }
    }
}

/// The usage panel in the menu: each agent's plan limits (or API spend), one
/// line per meter, the same layout for every agent.
struct Dashboard: View {
    enum Part { case all, main, codex }
    let u: Usage
    var part: Part = .all
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            if part != .codex { if u.mode == "plan" { claude } else { spend } }
            if part == .all { linkRow(Dashboard.link(u)?.title) }
            if part != .main, let c = u.codex { codex(c) }
            if part == .all, u.codex != nil { linkRow(Dashboard.codexLink.title) }
        }
        .padding(.horizontal, 14).padding(.vertical, part == .all ? 10 : 6)
        .frame(width: 300, alignment: .leading)
    }

    /// Each tile's official usage page, shown as a link under it.
    static func link(_ u: Usage) -> (title: String, url: String)? {
        switch u.mode {
        case "plan": return ("Usage and credits on claude.ai ↗", "https://claude.ai/settings/usage")
        case "anthropic": return ("Cost in Claude Console ↗", "https://platform.claude.com/cost")
        case "estimate": return ("Your API usage in Claude Console ↗", "https://platform.claude.com/usage")
        case "openai": return ("Usage on platform.openai.com ↗", "https://platform.openai.com/usage")
        default: return nil
        }
    }
    static let codexLink = (title: "Usage on chatgpt.com ↗", url: "https://chatgpt.com/codex/settings/usage")

    /// What a link row looks like in the review renders (the menu uses real menu items).
    @ViewBuilder func linkRow(_ title: String?) -> some View {
        if let title { Text(title).font(.system(size: 13)).padding(.top, -6) }
    }

    func pctText(_ p: Double) -> String { "\(Int(p.rounded(.down)))%" }

    /// Session and week rows for a plan; a limit that's been hit turns its bar red.
    @ViewBuilder func limits(session: Usage.Window?, weekly: Usage.Window?, outdated: Bool = false) -> some View {
        VStack(alignment: .leading, spacing: 5) {
            if let s = session, s.active == true, let p = s.pct {
                MeterRow(label: "5-hour", pct: p, value: pctText(p), note: s.resets_at.map(clock) ?? "")
            } else if outdated {
                Text("Your limits have reset since this reading. Run /usage in Claude Code to update it.")
                    .font(.system(size: 11.5)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            } else {
                MeterRow(label: "5-hour", pct: 0, value: "0%", note: "not started")
            }
            if let w = weekly, let p = w.pct {
                MeterRow(label: "Week", pct: p, value: pctText(p), note: w.resets_at.map(dayClock) ?? "")
            }
        }
    }

    @ViewBuilder var claude: some View {
        VStack(alignment: .leading, spacing: 6) {
            PanelHeader(title: "Claude Code · \(u.plan?.label ?? "Claude plan")", age: u.account?.as_of)
            if let a = u.account {
                limits(session: a.session, weekly: a.weekly, outdated: a.outdated == true)
                if u.status == "credits", let c = a.credits {
                    Label("On usage credits · \(money(c.used ?? 0)) used" + (c.limit.map { " of \(money($0))" } ?? ""), systemImage: "creditcard.fill")
                        .font(.system(size: 11.5, weight: .medium)).foregroundStyle(.blue)
                } else if u.status == "warn", let eta = a.eta {
                    Text("At this pace you hit the limit around \(clock(eta))").font(.system(size: 11.5)).foregroundStyle(amber)
                }
            } else {
                Text("No reading yet. Run /usage in Claude Code.").font(.system(size: 11.5)).foregroundStyle(.secondary)
            }
        }
    }

    @ViewBuilder func codex(_ c: Usage.Codex) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            PanelHeader(title: "Codex · \(c.plan)", age: c.as_of)
            limits(session: c.session, weekly: c.weekly)
        }
    }

    var caption: String {
        switch u.mode {
        case "anthropic": return "Anthropic API"
        case "openai": return "OpenAI API"
        default: return "Pay as you go"
        }
    }

    @ViewBuilder var spend: some View {
        VStack(alignment: .leading, spacing: 6) {
            PanelHeader(title: "\(caption) · \(monthName)")
            if let spend = u.spend {
                if let budget = u.budget, budget > 0 {
                    MeterRow(label: "Budget", pct: spend / budget * 100, value: shortMoney(spend), note: "of \(shortMoney(budget))")
                } else {
                    MeterRow(label: "Month", pct: nil, value: shortMoney(spend), note: "no budget")
                }
                if let t = u.today_spend {
                    MeterRow(label: "Today", pct: nil, value: shortMoney(t), note: u.spike.map { "\(Int($0))× usual" } ?? "")
                }
                if u.status == "over" {
                    Label(u.pass_until != nil ? "Pass until \(clock(u.pass_until!))" : "Budget reached · new prompts paused",
                          systemImage: u.pass_until != nil ? "ticket.fill" : "nosign")
                        .font(.system(size: 11.5, weight: .medium)).foregroundStyle(u.pass_until != nil ? amber : lampRed)
                } else if let p = u.projected {
                    Text("On pace for \(money(p))" + (u.over_at.map { ", over by \(shortDate($0))" } ?? ""))
                        .font(.system(size: 11)).foregroundStyle(u.over_at != nil ? amber : .secondary)
                }
                if u.mode == "estimate" { Text("At API prices, from Claude Code logs on this Mac").font(.system(size: 10.5)).foregroundStyle(.tertiary) }
                else if u.api?.stale == true { Text("Last known. Could not refresh.").font(.system(size: 10.5)).foregroundStyle(.tertiary) }
            } else {
                Text(u.api?.error ?? "No data yet").font(.system(size: 11.5)).foregroundStyle(.secondary)
            }
        }
    }

    var monthName: String {
        guard let m = u.month else { return "" }
        let f = DateFormatter(); f.dateFormat = "yyyy-MM"
        guard let d = f.date(from: m) else { return m }
        f.dateFormat = "MMMM"; return f.string(from: d)
    }
}

// MARK: - Menu bar: two items from one app

/// Sessions and Usage each have their own menu bar item and menu: the dots
/// open the session list, the gate opens the usage panel. Both come from this
/// one app and share its data, Settings window and Quit.
@MainActor
final class MenuBar: NSObject, NSMenuDelegate {
    // macOS puts newer items to the left, so the gate is made first: the dots
    // sit to its left. Both always exist; the gate is hidden while Usage is off.
    let usageItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
    let sessionsItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
    let sessionsMenu = NSMenu(), usageMenu = NSMenu()
    var state = SessionState(now: 0, rows: [], counts: [:])
    var usage: Usage?
    var lastUsage = Date.distantPast
    var watcher: DispatchSourceFileSystemObject?
    var timers: [Timer] = []
    var pending = false
    var appearance: NSKeyValueObservation?
    let openSettings: (String?) -> Void

    init(openSettings: @escaping (String?) -> Void) {
        self.openSettings = openSettings
        super.init()
        usageItem.autosaveName = "signals.usage"
        sessionsItem.autosaveName = "signals.sessions"
        usageItem.button?.imagePosition = .imageLeading
        usageItem.button?.font = NSFont.monospacedDigitSystemFont(ofSize: 13, weight: .regular)
        usageItem.isVisible = false
        for (item, menu) in [(sessionsItem, sessionsMenu), (usageItem, usageMenu)] {
            menu.delegate = self
            menu.autoenablesItems = false
            item.menu = menu
        }
        redraw()
        // The dots and gate are drawn in the menu bar's text colour, which
        // changes with light and dark menu bars.
        appearance = sessionsItem.button?.observe(\.effectiveAppearance) { [weak self] _, _ in Task { @MainActor in self?.redraw() } }
        watch()
        timers = [Timer.scheduledTimer(withTimeInterval: 15, repeats: true) { [weak self] _ in Task { @MainActor in self?.refreshSessions() } },
                  Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { [weak self] _ in Task { @MainActor in self?.refreshUsage() } }]
        refreshSessions()
        refreshUsage()
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound]) { _, _ in }
    }

    /// Hooks write a file per session; refresh as soon as the folder changes.
    func watch() {
        try? FileManager.default.createDirectory(at: Engine.sessionsDir, withIntermediateDirectories: true)
        let fd = open(Engine.sessionsDir.path, O_EVTONLY)
        guard fd >= 0 else { return }
        let src = DispatchSource.makeFileSystemObjectSource(fileDescriptor: fd, eventMask: [.write, .delete, .rename], queue: .main)
        src.setEventHandler { [weak self] in Task { @MainActor in self?.refreshSoon() } }
        src.setCancelHandler { close(fd) }
        src.resume()
        watcher = src
    }

    func refreshSoon() {
        guard !pending else { return }
        pending = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.25) { [weak self] in
            Task { @MainActor in self?.pending = false; self?.refreshSessions() }
        }
    }

    func refreshSessions() {
        Task {
            let (status, out, _) = await Engine.run(Engine.stateScript)
            guard status == 0, let data = out.data(using: .utf8),
                  let s = try? JSONDecoder().decode(SessionState.self, from: data) else { return }
            state = s
            redraw()
        }
    }

    func refreshUsage() {
        lastUsage = Date()
        Task {
            let (status, out, _) = await Engine.run(Engine.meter, env: ["SIGNALS_APP": "1"])
            guard status == 0, let data = out.data(using: .utf8), let u = try? JSONDecoder().decode(Usage.self, from: data) else { return }
            usage = u
            redraw()
            if let n = u.notify { notify(n) }
        }
    }

    func redraw() {
        guard let b = sessionsItem.button else { return }
        let dark = b.effectiveAppearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
        let fg = dark ? NSColor(white: 1, alpha: 0.92) : NSColor(white: 0, alpha: 0.85)
        b.image = StatusArt.image(rows: state.rows, usage: nil, fg: fg)
        b.toolTip = state.rows.isEmpty ? "Signals: no agent sessions" : "Signals: " + summary(state)
        if let gate = StatusArt.gateImage(usage, fg: fg), let ub = usageItem.button {
            ub.image = gate
            ub.attributedTitle = StatusArt.attributedTitle(usage, fg: fg)
            ub.toolTip = usageTip
            usageItem.isVisible = true
        } else {
            usageItem.isVisible = false
        }
    }

    /// One line for the menu's header, e.g. "Claude 5% · Codex 0%".
    var usageSummary: String? {
        guard let u = usage, u.on else { return nil }
        func limits(_ name: String, _ s: Usage.Window?, _ w: Usage.Window?) -> String {
            "\(name) 5h \(Int((s?.active ?? false) ? s?.pct ?? 0 : 0))%" + (w?.pct.map { " · wk \(Int($0))%" } ?? "")
        }
        var parts: [String] = []
        if u.mode == "plan", let a = u.account { parts.append(limits("Claude", a.session, a.weekly)) }
        if u.mode != "plan", let sp = u.spend { parts.append(shortMoney(sp) + (u.budget.map { " of \(shortMoney($0))" } ?? "")) }
        if let c = u.codex { parts.append(limits("Codex", c.session, c.weekly)) }
        return parts.isEmpty ? nil : parts.joined(separator: "   ")
    }

    var usageTip: String {
        guard let u = usage, u.on else { return "Signals: usage" }
        var lines = ["Signals usage"]
        if u.mode == "plan", let s = u.account?.session?.pct { lines.append("Claude: 5-hour \(Int(s))%" + (u.account?.weekly?.pct.map { ", week \(Int($0))%" } ?? "")) }
        if u.mode != "plan", let sp = u.spend { lines.append("Spend this month: \(shortMoney(sp))") }
        if let c = u.codex { lines.append("Codex: 5-hour \(Int(c.session?.pct ?? 0))%" + (c.weekly?.pct.map { ", week \(Int($0))%" } ?? "")) }
        let f = StatusArt.figure(u)
        if !f.text.isEmpty, u.mode == "plan" {
            var which = "Claude's 5-hour window"
            if let a = u.account, let x = StatusArt.closest(session: a.session, weekly: a.weekly), x.label == "wk" { which = "Claude's week" }
            if let c = u.codex, let x = StatusArt.closest(session: c.session, weekly: c.weekly),
               x.pct > (u.account.flatMap { StatusArt.closest(session: $0.session, weekly: $0.weekly)?.pct } ?? -1) {
                which = x.label == "wk" ? "Codex's week" : "Codex's 5-hour window"
            }
            lines.append("In the menu bar: \(f.text) of \(which)")
        }
        return lines.joined(separator: "\n")
    }

    func summary(_ s: SessionState) -> String {
        let parts: [(String, String)] = [("waiting", "needs you"), ("working", "working"), ("error", "stopped"), ("done", "done")]
        return parts.compactMap { k, label in s.counts[k].map { "\($0) \(label)" } }.joined(separator: " · ")
    }

    func notify(_ n: Usage.Note) {
        let c = UNMutableNotificationContent()
        c.title = n.title.replacingOccurrences(of: "🚧 ", with: "").replacingOccurrences(of: "⚠️ ", with: "")
            .replacingOccurrences(of: "⛔️ ", with: "").replacingOccurrences(of: "💳 ", with: "")
        c.body = n.body; c.sound = .default
        UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: UUID().uuidString, content: c, trigger: nil))
    }

    func menuNeedsUpdate(_ menu: NSMenu) {
        if menu === usageMenu {
            if Date().timeIntervalSince(lastUsage) > 20 { refreshUsage() }
            buildUsage()
        } else {
            buildSessions()
        }
    }

    @discardableResult
    func add(_ menu: NSMenu, _ title: String, _ action: Selector?, key: String = "", object: Any? = nil, image: NSImage? = nil) -> NSMenuItem {
        let m = NSMenuItem(title: title, action: action, keyEquivalent: key)
        m.target = self; m.representedObject = object; m.image = image
        m.isEnabled = action != nil
        menu.addItem(m)
        return m
    }
    func caption(_ menu: NSMenu, _ text: String) {
        let m = NSMenuItem()
        m.attributedTitle = NSAttributedString(string: text, attributes: [.font: NSFont.systemFont(ofSize: 11, weight: .semibold),
                                                                          .foregroundColor: NSColor.secondaryLabelColor, .kern: 0.4])
        m.isEnabled = false
        menu.addItem(m)
    }
    /// Settings, About and Quit, at the foot of both menus.
    func footer(_ menu: NSMenu, settingsPage: String?) {
        menu.addItem(.separator())
        add(menu, "Settings…", settingsPage == "usage" ? #selector(showUsageSettings) : #selector(showSettings), key: ",")
        add(menu, "About Signals", #selector(about))
        menu.addItem(NSMenuItem(title: "Quit Signals", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q"))
    }

    /// Money per session, only where money is what's counted (never on a plan).
    var costs: [String: Double] {
        guard let u = usage, u.on, u.costsSessions else { return [:] }
        return Dictionary((u.sessions ?? []).compactMap { s in s.sid.map { ($0, s.cost) } }, uniquingKeysWith: +)
    }

    func buildSessions() {
        let menu = sessionsMenu
        menu.removeAllItems()
        let head = NSMenuItem()
        let title = NSMutableAttributedString(string: "Sessions", attributes: [.font: NSFont.boldSystemFont(ofSize: 13)])
        if !state.rows.isEmpty {
            title.append(NSAttributedString(string: "   " + summary(state),
                                            attributes: [.font: NSFont.systemFont(ofSize: 12), .foregroundColor: NSColor.secondaryLabelColor]))
        }
        head.attributedTitle = title; head.isEnabled = false
        menu.addItem(head)
        menu.addItem(.separator())
        if state.rows.isEmpty {
            add(menu, "No agent sessions", nil)
            let hint = NSMenuItem()
            hint.attributedTitle = NSAttributedString(string: "Start one and its light appears here",
                                                      attributes: [.font: NSFont.systemFont(ofSize: 11), .foregroundColor: NSColor.secondaryLabelColor])
            hint.isEnabled = false
            menu.addItem(hint)
        }
        let costs = self.costs
        for r in state.rows {
            let m = add(menu, r.name, #selector(openFolder(_:)), object: r.cwd, image: StatusArt.dot(r.status))
            let t = NSMutableAttributedString(string: r.name + "\n", attributes: [.font: NSFont.systemFont(ofSize: 13)])
            var sub = "\(r.who) · \(r.label)" + (r.detail.map { " (\($0))" } ?? "") + " · \(ago(r.updated))"
            if let sid = r.session_id, let c = costs[sid] { sub += " · \(money(c))" }
            t.append(NSAttributedString(string: sub, attributes: [.font: NSFont.systemFont(ofSize: 11), .foregroundColor: NSColor.secondaryLabelColor]))
            m.attributedTitle = t
            m.toolTip = r.cwd
            m.isEnabled = !r.cwd.isEmpty
        }
        footer(menu, settingsPage: nil)
    }

    func buildUsage() {
        let menu = usageMenu
        menu.removeAllItems()
        let head = NSMenuItem()
        let title = NSMutableAttributedString(string: "Usage", attributes: [.font: NSFont.boldSystemFont(ofSize: 13)])
        if let s = usageSummary {
            title.append(NSAttributedString(string: "   " + s, attributes: [.font: NSFont.systemFont(ofSize: 12), .foregroundColor: NSColor.secondaryLabelColor]))
        }
        head.attributedTitle = title; head.isEnabled = false
        menu.addItem(head)
        menu.addItem(.separator())
        guard let u = usage, u.on else { add(menu, "Usage is off", nil); footer(menu, settingsPage: "usage"); return }
        func tile(_ part: Dashboard.Part, _ link: (title: String, url: String)?) {
            let item = NSMenuItem()
            let host = NSHostingView(rootView: Dashboard(u: u, part: part))
            host.frame = NSRect(origin: .zero, size: host.fittingSize)
            item.view = host
            menu.addItem(item)
            if let link { add(menu, link.title, #selector(openURL(_:)), object: link.url) }
        }
        tile(.main, Dashboard.link(u))
        if u.codex != nil { menu.addItem(.separator()); tile(.codex, Dashboard.codexLink) }
        // Sessions that cost money today but aren't running any more.
        let live = Set(state.rows.compactMap(\.session_id))
        let earlier = (u.costsSessions ? u.sessions ?? [] : []).filter { !live.contains($0.sid ?? "") }.prefix(5)
        if !earlier.isEmpty {
            menu.addItem(.separator())
            caption(menu, "EARLIER TODAY")
            for s in earlier {
                let name = s.title ?? URL(fileURLWithPath: s.cwd ?? "").lastPathComponent
                let t = NSMutableAttributedString(string: String(name.prefix(36)) + "  ", attributes: [.font: NSFont.systemFont(ofSize: 13)])
                t.append(NSAttributedString(string: money(s.cost), attributes: [.font: NSFont.monospacedDigitSystemFont(ofSize: 12, weight: .medium)]))
                if let x = s.times {
                    t.append(NSAttributedString(string: "  \(String(format: "%.1f", x))× usual",
                                                attributes: [.font: NSFont.systemFont(ofSize: 11), .foregroundColor: NSColor.systemOrange]))
                }
                let m = add(menu, name, #selector(openFolder(_:)), object: s.cwd, image: StatusArt.dot(s.state ?? "done"))
                m.attributedTitle = t; m.toolTip = s.cwd
                m.isEnabled = !(s.cwd ?? "").isEmpty
            }
        }
        if u.mode != "plan" {
            menu.addItem(.separator())
            if u.status == "over", u.mode != "openai" {
                if u.pass_until != nil { add(menu, "Cancel pass", #selector(cancelPass)) }
                else { add(menu, "Pay the toll: 1-hour pass", #selector(pass)) }
            }
            add(menu, "Set budget…", #selector(setBudget))
        }
        footer(menu, settingsPage: "usage")
    }

    @objc func showSettings() { openSettings(nil) }
    @objc func showUsageSettings() { openSettings("usage") }
    @objc func openURL(_ s: NSMenuItem) { if let u = s.representedObject as? String, let url = URL(string: u) { NSWorkspace.shared.open(url) } }
    @objc func openFolder(_ s: NSMenuItem) { if let p = s.representedObject as? String, !p.isEmpty { NSWorkspace.shared.open(URL(fileURLWithPath: p)) } }
    @objc func pass() { Task { _ = await Engine.signals(["pass", "1h"]); refreshUsage() } }
    @objc func cancelPass() { Task { _ = await Engine.signals(["pass", "off"]); refreshUsage() } }
    @objc func about() {
        NSApp.activate(ignoringOtherApps: true)
        NSApp.orderFrontStandardAboutPanel(options: [
            .credits: NSAttributedString(string: "Made by Sam Hudson\n\nSignals is an independent app, not made by or affiliated with Anthropic, OpenAI, Cursor, Google or GitHub. Claude and Claude Code are trademarks of Anthropic.",
                                         attributes: [.font: NSFont.systemFont(ofSize: 11), .foregroundColor: NSColor.secondaryLabelColor]),
        ])
    }
    @objc func setBudget() {
        let alert = NSAlert()
        alert.messageText = "Monthly budget"
        alert.informativeText = "In US dollars. Leave empty for no budget."
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 220, height: 24))
        field.placeholderString = "e.g. 50"
        if let b = usage?.budget { field.stringValue = b == b.rounded() ? String(Int(b)) : String(b) }
        alert.accessoryView = field
        alert.addButton(withTitle: "Save"); alert.addButton(withTitle: "Cancel")
        NSApp.activate(ignoringOtherApps: true)
        alert.window.initialFirstResponder = field
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        let v = field.stringValue.trimmingCharacters(in: .whitespaces).replacingOccurrences(of: "$", with: "")
        Task { _ = await Engine.signals(["budget", v.isEmpty ? "off" : v]); refreshUsage() }
    }
}

// MARK: - Window model

struct Agent: Identifiable, Decodable {
    let id: String, name: String
    var detected: Bool, installed: Bool
    var symbol: String {
        switch id {
        case "claude": return "sparkle"
        case "codex": return "terminal.fill"
        case "gemini": return "diamond.fill"
        case "cursor": return "cursorarrow.rays"
        default: return "chevron.left.forwardslash.chevron.right"
        }
    }
}

enum Mode: String, CaseIterable, Identifiable {
    case off, plan, anthropic, openai, estimate
    var id: String { rawValue }
    var title: String {
        switch self {
        case .off: return "Off"
        case .plan: return "Claude plan"
        case .anthropic: return "Anthropic API"
        case .openai: return "OpenAI API"
        case .estimate: return "Pay as you go"
        }
    }
    var subtitle: String {
        switch self {
        case .off: return "Just the session lights. You can turn usage on here any time."
        case .plan: return "Pro or Max. See how much of your session and weekly limits you have used."
        case .anthropic: return "Your API bill against a budget, with a cost for every session."
        case .openai: return "Your OpenAI API bill against a budget."
        case .estimate: return "On an API key? What each session on this Mac costs, priced from your Claude Code logs."
        }
    }
    var symbol: String {
        switch self {
        case .off: return "circle.slash"
        case .plan: return "person.crop.circle.badge.checkmark"
        case .anthropic: return "key.fill"
        case .openai: return "key.horizontal.fill"
        case .estimate: return "chart.bar.xaxis"
        }
    }
    var usesKey: Bool { self == .anthropic || self == .openai }
}

/// The window's pages. First run walks through all of them; Settings shows
/// Sessions and Usage as tabs.
enum Page: Int { case welcome, sessions, usage, configure, tutorial, done }

@MainActor
final class Setup: ObservableObject {
    @Published var welcome = false
    @Published var page: Page = .sessions
    // Sessions
    @Published var agents: [Agent] = []
    @Published var agentBusy: String?
    @Published var agentsChanged = false
    // Usage
    @Published var mode: Mode = .off
    @Published var savedMode: Mode = .off
    @Published var detected: String?
    @Published var detectedLabel: String?
    @Published var key = ""
    @Published var useSavedKey = true
    @Published var savedKeys: [String: Bool] = [:]
    @Published var budget = ""
    @Published var busy = false
    @Published var error: String?

    var hasSavedKey: Bool { savedKeys[mode.rawValue] ?? false }

    func load() async {
        let (s1, o1, _) = await Engine.signals(["_info"])
        if s1 == 0, let d = o1.data(using: .utf8), let list = try? JSONDecoder().decode([Agent].self, from: d) { agents = list }
        else { error = "Couldn't read your agents. Is Signals installed?" }
        let (s2, o2, _) = await Engine.signals(["_usage_info"])
        guard s2 == 0, let d = o2.data(using: .utf8), let info = try? JSONSerialization.jsonObject(with: d) as? [String: Any] else { return }
        detected = info["detected_plan"] as? String
        detectedLabel = info["detected_label"] as? String
        savedKeys = info["saved_keys"] as? [String: Bool] ?? [:]
        savedMode = (info["mode"] as? String).flatMap(Mode.init(rawValue:)) ?? .off
        mode = savedMode
        if let b = info["budget"] as? Double { budget = b == b.rounded() ? String(Int(b)) : String(b) }
    }

    func setAgent(_ agent: Agent, on: Bool) async {
        agentBusy = agent.id; error = nil
        defer { agentBusy = nil }
        let (status, _, err) = await Engine.signals([on ? "install" : "uninstall", agent.id, "--no-app"])
        if status != 0 { error = Engine.clean(err).isEmpty ? "That didn't work (code \(status))." : Engine.clean(err) }
        else { agentsChanged = true }
        let (s, o, _) = await Engine.signals(["_info"])
        if s == 0, let d = o.data(using: .utf8), let list = try? JSONDecoder().decode([Agent].self, from: d) { agents = list }
    }

    /// Saves the usage choices through the CLI. Returns true on success.
    func apply() async -> Bool {
        busy = true; error = nil
        defer { busy = false }
        var steps: [([String], [String: String])] = []
        switch mode {
        case .off:
            if savedMode == .off { return true }
            steps.append((["usage", "off"], [:]))
        case .plan:
            guard detectedLabel != nil else { error = "Sign in to Claude Code first (run claude, then /login)."; return false }
            steps.append((["usage", "plan"], [:]))
        case .anthropic, .openai:
            if hasSavedKey && useSavedKey { steps.append((["usage", mode.rawValue], ["SIGNALS_USE_SAVED_KEY": "1"])) }
            else {
                let k = key.trimmingCharacters(in: .whitespacesAndNewlines)
                guard !k.isEmpty else { error = "Paste your admin key to continue."; return false }
                steps.append((["usage", mode.rawValue], ["SIGNALS_KEY": k]))
            }
        case .estimate:
            steps.append((["usage", "estimate"], [:]))
        }
        if mode != .plan && mode != .off {
            let b = budget.trimmingCharacters(in: .whitespaces).replacingOccurrences(of: "$", with: "")
            guard b.isEmpty || Double(b) != nil else { error = "The budget should be a number of US dollars."; return false }
            steps.append((["budget", b.isEmpty ? "off" : b], [:]))
        }
        for (args, env) in steps {
            let (status, _, err) = await Engine.signals(args, env: env)
            if status != 0 { error = Engine.clean(err).isEmpty ? "That didn't finish (code \(status))." : Engine.clean(err); return false }
        }
        key = ""
        savedMode = mode
        return true
    }
}

// MARK: - Illustrations

struct SignalArt: View {
    var fixed: Int? = nil   // 0 red, 1 amber, 2 green, for still renders
    var body: some View {
        TimelineView(.animation) { ctx in
            let lit = fixed ?? Self.lit(ctx.date)
            Canvas { gc, size in Self.draw(gc, size, lit: lit) }
        }
        .aspectRatio(0.5, contentMode: .fit)
    }
    static func lit(_ date: Date) -> Int {
        let t = date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: 6) / 6
        return t < 0.33 ? 0 : (t < 0.66 ? 1 : 2)
    }
    static func draw(_ gc: GraphicsContext, _ size: CGSize, lit: Int) {
        let u = size.width / 100
        func r(_ x: Double, _ y: Double, _ w: Double, _ h: Double) -> CGRect { CGRect(x: x * u, y: y * u, width: w * u, height: h * u) }
        gc.fill(Path(r(44, 160, 12, 40)), with: .color(Color(red: 0.24, green: 0.26, blue: 0.32)))
        let housing = Path(roundedRect: r(14, 4, 72, 160), cornerRadius: 20 * u)
        gc.fill(housing, with: .color(Color(red: 0.2, green: 0.22, blue: 0.27)))
        gc.stroke(housing, with: .color(.white.opacity(0.14)), lineWidth: 1.5 * u)
        let colors = [lampRed, lampAmber, lampGreen]
        for i in 0..<3 {
            let y = 14 + Double(i) * 50
            gc.fill(Path(roundedRect: r(20, y - 4, 60, 8), cornerRadius: 3 * u), with: .color(Color(white: 0.08)))
            let lens = r(26, y + 4, 48, 34), on = i == lit
            if on { gc.fill(Path(ellipseIn: lens.insetBy(dx: -8 * u, dy: -8 * u)), with: .color(colors[i].opacity(0.28))) }
            gc.fill(Path(ellipseIn: CGRect(x: lens.midX - 17 * u, y: lens.midY - 17 * u, width: 34 * u, height: 34 * u)),
                    with: .color(on ? colors[i] : colors[i].opacity(0.16)))
        }
    }
}

/// The gate: a booth and a barrier arm that lifts, lamp red while closed.
struct GateArt: View {
    var fixedOpen: Double? = nil   // for still renders
    var body: some View {
        TimelineView(.animation) { ctx in
            let open = fixedOpen ?? Self.openAmount(ctx.date)
            Canvas { gc, size in Self.draw(gc, size, open: open) }
        }
        .aspectRatio(1.6, contentMode: .fit)
    }
    /// 6-second cycle: closed, lifting, open, dropping.
    static func openAmount(_ date: Date) -> Double {
        let t = date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: 6) / 6
        func ease(_ x: Double) -> Double { x * x * (3 - 2 * x) }
        switch t {
        case ..<0.35: return 0
        case ..<0.45: return ease((t - 0.35) / 0.1)
        case ..<0.80: return 1
        case ..<0.90: return 1 - ease((t - 0.8) / 0.1)
        default: return 0
        }
    }
    static func draw(_ gc: GraphicsContext, _ size: CGSize, open: Double) {
        let u = size.width / 320   // design units: 320 x 200
        func r(_ x: Double, _ y: Double, _ w: Double, _ h: Double) -> CGRect { CGRect(x: x * u, y: y * u, width: w * u, height: h * u) }
        gc.fill(Path(r(0, 168, 320, 32)), with: .color(Color(white: 0.1)))
        for i in 0..<5 { gc.fill(Path(roundedRect: r(130 + Double(i) * 40, 182, 24, 4), cornerRadius: 2 * u), with: .color(.white.opacity(0.12))) }
        gc.fill(Path(roundedRect: r(24, 52, 70, 118), cornerRadius: 8 * u), with: .color(Color(red: 0.22, green: 0.24, blue: 0.3)))
        gc.stroke(Path(roundedRect: r(24, 52, 70, 118), cornerRadius: 8 * u), with: .color(.white.opacity(0.15)), lineWidth: 1.5 * u)
        gc.fill(Path(roundedRect: r(16, 38, 86, 18), cornerRadius: 5 * u), with: .color(amber))
        gc.fill(Path(roundedRect: r(36, 66, 46, 36), cornerRadius: 4 * u), with: .color(Color(red: 0.06, green: 0.17, blue: 0.24)))
        gc.fill(Path(roundedRect: r(39, 69, 18, 30), cornerRadius: 2 * u), with: .color(Color(red: 0.35, green: 0.78, blue: 0.98).opacity(0.3)))
        let lamp = open > 0.5 ? lampGreen : lampRed
        gc.fill(Path(ellipseIn: r(47, 118, 24, 24).insetBy(dx: -6 * u, dy: -6 * u)), with: .color(lamp.opacity(0.25)))
        gc.fill(Path(ellipseIn: r(51, 122, 16, 16)), with: .color(lamp))
        gc.fill(Path(roundedRect: r(100, 132, 14, 38), cornerRadius: 3 * u), with: .color(Color(red: 0.28, green: 0.3, blue: 0.36)))
        var arm = gc
        let hinge = CGPoint(x: 107 * u, y: 138 * u)
        arm.translateBy(x: hinge.x, y: hinge.y)
        arm.rotate(by: .degrees(-72 * open))
        let capsule = Path(roundedRect: CGRect(x: 0, y: -5 * u, width: 150 * u, height: 10 * u), cornerRadius: 5 * u)
        arm.fill(capsule, with: .color(.white))
        arm.clip(to: capsule)
        for i in stride(from: 0.0, to: 150.0, by: 20.0) { arm.fill(Path(r(i + 10, -5, 10, 10)), with: .color(lampRed)) }
        gc.fill(Path(ellipseIn: CGRect(x: hinge.x - 6 * u, y: hinge.y - 6 * u, width: 12 * u, height: 12 * u)), with: .color(Color(white: 0.85)))
    }
}

/// The signal and the gate on a night sky, for the welcome and done pages.
struct Hero: View {
    var still = false
    var open: Double? = nil
    var body: some View {
        ZStack(alignment: .bottom) {
            night
            Rectangle().fill(Color(white: 0.1)).frame(height: 40)
            HStack(alignment: .bottom, spacing: 70) {
                SignalArt(fixed: still ? 2 : nil).frame(height: 190).padding(.bottom, 4)
                GateArt(fixedOpen: still ? (open ?? 0) : open).frame(width: 300)
            }
            .padding(.top, 30)
        }
        .frame(height: 290)
    }
}

// MARK: - Window pieces

struct StepDots: View {
    let page: Page
    var body: some View {
        HStack(spacing: 6) {
            ForEach(1..<5) { i in
                Capsule().fill(i <= page.rawValue ? amber : Color.secondary.opacity(0.25)).frame(width: i == page.rawValue ? 22 : 8, height: 8)
            }
        }
        .animation(.easeInOut(duration: 0.25), value: page)
    }
}

struct PageHeader: View {
    let page: Page, title: String, subtitle: String
    var dots = true
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if dots { StepDots(page: page) }
            Text(title).font(.system(size: 26, weight: .bold))
            Text(subtitle).font(.system(size: 14)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct AgentRow: View {
    let agent: Agent, busy: Bool
    let toggle: (Bool) -> Void
    var body: some View {
        HStack(spacing: 16) {
            Image(systemName: agent.symbol).font(.system(size: 18, weight: .medium))
                .foregroundStyle(agent.installed ? Color.primary : .secondary).frame(width: 28)
            VStack(alignment: .leading, spacing: 4) {
                Text(agent.name).font(.system(size: 14, weight: .semibold))
                Text(agent.installed ? "Lights on. New sessions show in your menu bar."
                     : agent.detected ? "Found on this Mac. Turn on to show its sessions."
                     : "Not found on this Mac. You can turn it on before you install it.")
                    .font(.system(size: 12)).foregroundStyle(.secondary)
            }
            Spacer(minLength: 12)
            if busy { ProgressView().controlSize(.small) }
            Toggle("", isOn: Binding(get: { agent.installed }, set: toggle)).toggleStyle(.switch).labelsHidden().disabled(busy)
        }
        .padding(.vertical, 11).padding(.horizontal, 18)
    }
}

struct OptionCard: View {
    let mode: Mode, selected: Bool, badge: String?
    var body: some View {
        HStack(alignment: .center, spacing: 14) {
            Image(systemName: mode.symbol).font(.system(size: 20, weight: .medium))
                .foregroundStyle(selected ? amber : .secondary).frame(width: 30)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 8) {
                    Text(mode.title).font(.system(size: 14.5, weight: .semibold))
                    if let badge { Text(badge).font(.system(size: 11, weight: .semibold)).padding(.horizontal, 7).padding(.vertical, 2)
                        .background(Capsule().fill(amber.opacity(0.18))).foregroundStyle(amber) }
                }
                Text(mode.subtitle).font(.system(size: 12)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
            Image(systemName: selected ? "checkmark.circle.fill" : "circle").font(.system(size: 18))
                .foregroundStyle(selected ? amber : Color.secondary.opacity(0.4))
        }
        .padding(.horizontal, 14).padding(.vertical, 10)
        .background(RoundedRectangle(cornerRadius: 12).fill(Color.primary.opacity(selected ? 0.07 : 0.03)))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(selected ? amber : Color.primary.opacity(0.08), lineWidth: selected ? 1.5 : 1))
        .contentShape(Rectangle())
    }
}

/// A menu bar sample: session dots, the gate, and a figure.
struct BarChip: View {
    var dots: [String] = []
    var gate: NSColor? = nil
    var text = ""
    var body: some View {
        HStack(spacing: 5) {
            ForEach(Array(dots.enumerated()), id: \.offset) { _, s in Circle().fill(Color(nsColor: lampColor(s))).frame(width: 9, height: 9) }
            if let gate { Image(nsImage: StatusArt.gate(gate)).padding(.leading, dots.isEmpty ? 0 : 3) }
            if !text.isEmpty { Text(text).font(.system(size: 13, weight: .medium).monospacedDigit()).foregroundStyle(.white) }
        }
        .padding(.horizontal, 10).padding(.vertical, 5)
        .background(RoundedRectangle(cornerRadius: 6).fill(Color(white: 0.16)))
        .frame(width: 128, alignment: .leading)
    }
}

// MARK: - Pages

struct WelcomePage: View {
    var still = false
    var body: some View {
        VStack(spacing: 0) {
            Hero(still: still)
            VStack(spacing: 10) {
                Text("Welcome to Signals").font(.system(size: 30, weight: .bold))
                Text("See every AI agent session at a glance: who's working, who needs you,\nand who's done. Keep an eye on your Claude plan or API spend too.")
                    .font(.system(size: 14)).foregroundStyle(.secondary).multilineTextAlignment(.center).lineSpacing(2)
            }
            .padding(.top, 30)
            Spacer()
        }
    }
}

struct SessionsPage: View {
    @ObservedObject var s: Setup
    var body: some View {
        VStack(spacing: 18) {
            PageHeader(page: .sessions, title: "Your agents",
                       subtitle: "One light per session. The agents found on this Mac are already on.", dots: s.welcome)
            VStack(spacing: 0) {
                ForEach(Array(s.agents.enumerated()), id: \.element.id) { i, agent in
                    AgentRow(agent: agent, busy: s.agentBusy == agent.id) { on in Task { await s.setAgent(agent, on: on) } }
                    if i < s.agents.count - 1 { Divider().padding(.leading, 62) }
                }
            }
            .background(RoundedRectangle(cornerRadius: 12).fill(Color.primary.opacity(0.035)))
            .overlay(RoundedRectangle(cornerRadius: 12).stroke(Color.primary.opacity(0.08)))
            Group {
                if let e = s.error {
                    Label(e, systemImage: "exclamationmark.triangle.fill").foregroundStyle(lampRed)
                } else {
                    Text(s.agentsChanged ? "Restart running agent sessions so they pick up the change."
                                         : "Cursor and Copilot can't report \"needs you\", so they show red and green only.")
                        .foregroundStyle(.secondary)
                }
            }
            .font(.system(size: 12)).frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

struct UsagePage: View {
    @ObservedObject var s: Setup
    var body: some View {
        VStack(spacing: 16) {
            PageHeader(page: .usage, title: "Usage",
                       subtitle: "Optional. Signals can also show what your agents use: your plan's limits, or your API spend.", dots: s.welcome)
            VStack(spacing: 8) {
                ForEach(Mode.allCases.filter { [.off, .plan, .estimate].contains($0) || $0 == s.savedMode }) { m in
                    OptionCard(mode: m, selected: s.mode == m,
                               badge: m == .plan ? s.detectedLabel.map { "Signed in: \($0)" } : nil)
                        .onTapGesture { withAnimation(.easeOut(duration: 0.15)) { s.mode = m; s.error = nil } }
                }
            }
            if let e = s.error {
                Label(e, systemImage: "exclamationmark.triangle.fill").font(.system(size: 12.5))
                    .foregroundStyle(lampRed).frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }
}

struct ConfigurePage: View {
    @ObservedObject var s: Setup
    var body: some View {
        VStack(spacing: 20) {
            PageHeader(page: .configure, title: s.mode == .plan ? "Your Claude plan" : s.mode.title,
                       subtitle: s.mode == .plan ? "Your plan's limits are shared across claude.ai, the desktop app and Claude Code."
                                                 : "Set a monthly budget. Signals warns you at 80% and closes the gate at 100%.", dots: s.welcome)
            Form {
                if s.mode == .plan {
                    LabeledContent("Plan") {
                        VStack(alignment: .trailing, spacing: 2) {
                            Text(s.detectedLabel ?? "Not signed in").font(.system(size: 13, weight: .semibold))
                                .foregroundStyle(s.detectedLabel == nil ? lampRed : .primary)
                            Text(s.detectedLabel == nil ? "Sign in to Claude Code (run claude, then /login)"
                                                        : "From your Claude login. Follows it if your plan changes.")
                                .font(.system(size: 11)).foregroundStyle(.secondary)
                        }
                    }
                    VStack(alignment: .leading, spacing: 4) {
                        Text("Where your usage comes from")
                        Text("Signals reads the usage Claude Code already saves on this Mac, and the exact figures it gives its terminal status line. It never uses your Claude login and sends nothing anywhere.")
                            .font(.system(size: 11.5)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                        Text("For exact figures as you work in the terminal, run: signals install --statusline")
                            .font(.system(size: 11.5)).foregroundStyle(.secondary).textSelection(.enabled)
                    }
                } else {
                    if s.mode.usesKey {
                        if s.hasSavedKey {
                            Picker("Admin key", selection: $s.useSavedKey) {
                                Text("Use the saved key").tag(true); Text("Enter a new key").tag(false)
                            }
                            .pickerStyle(.radioGroup)
                        }
                        if !s.hasSavedKey || !s.useSavedKey {
                            SecureField(s.mode == .anthropic ? "sk-ant-admin01-…" : "sk-admin-…", text: $s.key)
                            Text(s.mode == .anthropic
                                 ? "An Admin API key from the Claude Console (Settings → Admin keys). Regular API keys can't read your bill."
                                 : "An admin key from the OpenAI dashboard. Regular API keys can't read your bill.")
                                .font(.system(size: 11.5)).foregroundStyle(.secondary)
                        }
                    }
                    TextField("Monthly budget in USD (optional)", text: $s.budget)
                    Text("Keys are stored in your macOS Keychain and checked with \(s.mode == .openai ? "OpenAI" : "Anthropic") before they're saved.")
                        .font(.system(size: 11.5)).foregroundStyle(.secondary)
                        .opacity(s.mode.usesKey ? 1 : 0)
                }
            }
            .formStyle(.grouped)
            .scrollContentBackground(.hidden)
            if let e = s.error {
                Label(e, systemImage: "exclamationmark.triangle.fill").font(.system(size: 12.5))
                    .foregroundStyle(lampRed).frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }
}

struct TutorialPage: View {
    @ObservedObject var s: Setup
    typealias Sample = (chip: BarChip, title: String, text: String)
    var rows: [Sample] {
        let fg = NSColor(white: 1, alpha: 0.92)
        let lights: Sample = (BarChip(dots: ["waiting", "working", "done"]), "Your sessions",
                              "One light per session: amber needs you, red is working, green is done. Click for the list.")
        switch s.mode {
        case .off:
            return [lights,
                    (BarChip(dots: ["working", "working"]), "Busy", "Two agents working. Nothing needs you yet."),
                    (BarChip(), "No sessions", "A small signal outline when nothing is running.")]
        case .plan:
            return [lights,
                    (BarChip(dots: ["done"], gate: fg, text: "42%"), "On track", "The gate, then how much of your 5-hour limit you've used. Once your week is nearly used up, it shows the week instead."),
                    (BarChip(dots: ["done"], gate: .systemOrange, text: "86%"), "Getting close", "Past 80%. Signals says when you'll hit the limit at this pace."),
                    (BarChip(dots: ["done"], gate: .systemRed, text: "100%"), "Limit reached", "Blocked until the limit resets. The menu says when you're back."),
                    (BarChip(dots: ["done"], gate: .systemBlue, text: "$4.20"), "On credits", "Past your plan on usage credits. The only time a plan shows money.")]
        default:
            return [lights,
                    (BarChip(dots: ["done"], gate: fg, text: "$42"), "On track", "The gate, then this month's spend on this Mac. Every session's cost is next to it in the session list."),
                    (BarChip(dots: ["done"], gate: .systemOrange, text: "$86"), "Getting close", "Past 80%, with the date you'll run out at this pace."),
                    (BarChip(dots: ["done"], gate: .systemRed, text: "$100"), "Gate down",
                     s.mode == .openai ? "Budget reached. Signals lets you know." : "Budget reached. New Claude Code prompts wait until you pay the toll."),
                    (BarChip(dots: ["done"], gate: .systemOrange, text: "$104"), "Paid the toll", "You chose to keep going: a one-hour pass, or a higher budget.")]
        }
    }
    var body: some View {
        VStack(spacing: 18) {
            PageHeader(page: .tutorial, title: "Reading your menu bar",
                       subtitle: s.mode == .off ? "Signals lives next to the clock." : "Session lights first, then the gate and your usage.")
            VStack(spacing: 0) {
                ForEach(Array(rows.enumerated()), id: \.offset) { i, row in
                    HStack(alignment: .center, spacing: 16) {
                        row.chip
                        VStack(alignment: .leading, spacing: 3) {
                            Text(row.title).font(.system(size: 14, weight: .semibold))
                            Text(row.text).font(.system(size: 12.5)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                        }
                        Spacer(minLength: 0)
                    }
                    .padding(.vertical, 10).padding(.horizontal, 14)
                    if i < rows.count - 1 { Divider().padding(.leading, 158) }
                }
            }
            .background(RoundedRectangle(cornerRadius: 12).fill(Color.primary.opacity(0.03)))
            .overlay(RoundedRectangle(cornerRadius: 12).stroke(Color.primary.opacity(0.08)))
            Text("Settings… in that menu brings you back here.").font(.system(size: 12)).foregroundStyle(.secondary)
        }
    }
}

struct DonePage: View {
    var still = false
    var body: some View {
        VStack(spacing: 0) {
            Hero(still: still, open: 1)
            VStack(spacing: 10) {
                Label("You're all set", systemImage: "checkmark.seal.fill").font(.system(size: 28, weight: .bold))
                    .foregroundStyle(lampGreen)
                Text("Look for Signals in your menu bar, next to the clock.").font(.system(size: 14)).foregroundStyle(.secondary)
            }
            .padding(.top, 30)
            Spacer()
        }
    }
}

// MARK: - Window

struct SetupView: View {
    @ObservedObject var s: Setup
    var still = false   // render mode: no animation, no loading
    var close: () -> Void = {}

    var body: some View {
        VStack(spacing: 0) {
            if !s.welcome {
                Picker("", selection: Binding(get: { s.page == .sessions ? 0 : 1 },
                                              set: { s.error = nil; s.page = $0 == 0 ? .sessions : .usage })) {
                    Text("Sessions").tag(0); Text("Usage").tag(1)
                }
                .pickerStyle(.segmented).labelsHidden().frame(width: 240)
                .padding(.top, 22)
            }
            Group {
                switch s.page {
                case .welcome: WelcomePage(still: still)
                case .sessions: SessionsPage(s: s).padding(.horizontal, 44).padding(.top, s.welcome ? 36 : 20)
                case .usage: UsagePage(s: s).padding(.horizontal, 44).padding(.top, s.welcome ? 30 : 20)
                case .configure: ConfigurePage(s: s).padding(.horizontal, 44).padding(.top, s.welcome ? 36 : 20)
                case .tutorial: TutorialPage(s: s).padding(.horizontal, 44).padding(.top, 36)
                case .done: DonePage(still: still)
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)

            Divider()
            HStack {
                if showBack { Button("Back") { back() }.keyboardShortcut(.cancelAction) }
                Spacer()
                if s.busy { ProgressView().controlSize(.small).padding(.trailing, 6) }
                Button(action: next) { Text(primary).frame(minWidth: 120) }
                    .keyboardShortcut(.defaultAction)
                    .buttonStyle(.borderedProminent).controlSize(.large)
                    .disabled(s.busy)
            }
            .padding(.horizontal, 24).padding(.vertical, 16)
        }
        .frame(width: windowSize.width, height: windowSize.height)
        .task { if !still { await s.load() } }
    }

    var showBack: Bool {
        s.welcome ? [.sessions, .usage, .configure, .tutorial].contains(s.page) : s.page == .configure
    }
    var primary: String {
        switch s.page {
        case .welcome: return "Get started"
        case .sessions: return s.welcome ? "Continue" : "Done"
        case .usage: return s.mode == .off ? (s.welcome ? "Continue" : "Save") : "Continue"
        case .configure: return s.welcome ? "Save and continue" : "Save"
        case .tutorial: return "Continue"
        case .done: return "Done"
        }
    }
    func go(_ p: Page) { s.error = nil; withAnimation(.easeInOut(duration: 0.2)) { s.page = p } }
    func back() {
        switch s.page {
        case .usage: go(.sessions)
        case .configure: go(.usage)
        case .tutorial: go(s.mode == .off ? .usage : .configure)
        default: break
        }
    }
    func next() {
        switch s.page {
        case .welcome: go(.sessions)
        case .sessions: if s.welcome { go(.usage) } else { close() }
        case .usage:
            if s.mode != .off { go(.configure); return }
            Task { if await s.apply() { if s.welcome { go(.tutorial) } else { close() } } }
        case .configure:
            Task { if await s.apply() { if s.welcome { go(.tutorial) } else { close() } } }
        case .tutorial: go(.done)
        case .done: close()
        }
    }
}

// MARK: - Rendering (for review and the app icon)

@MainActor
enum Renderer {
    static func png<V: View>(_ view: V, size: CGSize) -> Data? {
        let host = NSHostingView(rootView: view.frame(width: size.width, height: size.height))
        host.frame = NSRect(origin: .zero, size: size)
        let window = NSWindow(contentRect: host.frame, styleMask: [.borderless], backing: .buffered, defer: false)
        window.contentView = host
        host.layoutSubtreeIfNeeded(); host.display()
        guard let rep = host.bitmapImageRepForCachingDisplay(in: host.bounds) else { return nil }
        host.cacheDisplay(in: host.bounds, to: rep)
        return rep.representation(using: .png, properties: [:])
    }
    static func write(_ data: Data?, _ dir: URL, _ name: String) { if let data { try? data.write(to: dir.appendingPathComponent("\(name).png")) } }

    static let agents = [
        Agent(id: "claude", name: "Claude Code", detected: true, installed: true),
        Agent(id: "codex", name: "Codex", detected: true, installed: true),
        Agent(id: "gemini", name: "Gemini CLI", detected: false, installed: false),
        Agent(id: "cursor", name: "Cursor", detected: true, installed: true),
        Agent(id: "copilot", name: "Copilot (VS Code)", detected: false, installed: false),
    ]

    static func review(to dir: URL) {
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let shots: [(String, Page, Mode, Bool)] = [
            ("1-welcome", .welcome, .off, true), ("2-sessions", .sessions, .off, true), ("3-usage", .usage, .plan, true),
            ("4-configure-plan", .configure, .plan, true), ("4-configure-anthropic", .configure, .anthropic, true),
            ("5-tutorial-plan", .tutorial, .plan, true), ("5-tutorial-api", .tutorial, .estimate, true), ("5-tutorial-off", .tutorial, .off, true),
            ("6-done", .done, .off, true), ("7-settings-sessions", .sessions, .off, false), ("7-settings-usage", .usage, .estimate, false),
        ]
        for (name, page, mode, welcome) in shots {
            let s = Setup()
            s.welcome = welcome; s.page = page; s.mode = mode; s.detected = "pro"; s.detectedLabel = "Claude Pro"; s.agents = agents
            s.budget = mode == .plan ? "" : "100"
            write(png(SetupView(s: s, still: true).background(Color(nsColor: .windowBackgroundColor)), size: windowSize), dir, name)
        }
        usagePanels(to: dir)
        bars(to: dir)
    }

    static func fixtures() -> [String: String] {
        let now = Date().timeIntervalSince1970
        func plan(_ s: Double, _ w: Double, status: String, locked: Bool = false, credits: Bool = false, used: Double = 0) -> String {
            """
            {"mode":"plan","status":"\(status)","plan":{"label":"Claude Pro"},"limit_hits_week":\(locked ? 1 : 0),
             "account":{"session":{"pct":\(s),"resets_at":\(now + 5400),"active":true},"weekly":{"pct":\(w),"resets_at":\(now + 3 * 86400)},
              "locked":\(locked ? "\"session_limit\"" : "null"),"eta":\(status == "warn" ? "\(now + 1500)" : "null"),
              "credits":{"enabled":\(credits),"used":\(used),"limit":\(credits ? "50" : "null"),"reason":\(credits ? "null" : "\"out_of_credits\"")},
              "breakdown":[{"name":"Claude Code","pct":95},{"name":"Chats","pct":5}],"source":"claude","as_of":\(now - 720)}}
            """
        }
        func api(_ status: String, spend: Double, pass: Bool = false) -> String {
            """
            {"mode":"anthropic","status":"\(status)","month":"2026-10","spend":\(spend),"budget":100,"pct":\(Int(spend)),"today_spend":18.2,"spike":3,
             "projected":104.1,"over_at":\(now + 4 * 86400),"pass_until":\(pass ? "\(now + 3600)" : "null"),
             "sessions":[{"sid":"s1","cost":9.1,"cwd":"/code/acme/api","title":"Refactor billing service","state":"working","times":2.7},
                         {"sid":"s2","cost":2.3,"cwd":"/code/web","title":"Fix the login redirect","state":"done"}]}
            """
        }
        let codex = """
            {"mode":"plan","status":"ok","plan":{"label":"Claude Pro"},
             "account":{"session":{"pct":42,"resets_at":\(now + 5400),"active":true},"weekly":{"pct":15,"resets_at":\(now + 3 * 86400)},
              "credits":{"enabled":false,"reason":"out_of_credits"},"source":"claude","as_of":\(now - 300)},
             "codex":{"plan":"ChatGPT Plus","status":"warn","as_of":\(now - 60),
              "session":{"pct":84,"resets_at":\(now + 7200),"active":true},"weekly":{"pct":31,"resets_at":\(now + 4 * 86400)}}}
            """
        let claudeClosest = codex.replacingOccurrences(of: "\"pct\":84", with: "\"pct\":12").replacingOccurrences(of: "\"status\":\"warn\"", with: "\"status\":\"ok\"")
        return ["plan-claude-closest": claudeClosest, "plan-codex": codex, "plan-ok": plan(42, 15, status: "ok"), "plan-warn": plan(86, 30, status: "warn"),
                "plan-blocked": plan(100, 40, status: "blocked", locked: true),
                "plan-credits": plan(100, 40, status: "credits", locked: true, credits: true, used: 4.2),
                "api-warn": api("warn", spend: 86.4), "api-over": api("over", spend: 104.2), "api-pass": api("over", spend: 104.2, pass: true)]
    }

    /// The usage panel from the menu, in each state.
    static func usagePanels(to dir: URL) {
        for (name, json) in fixtures() where ["plan-ok", "plan-warn", "plan-blocked", "plan-credits", "api-warn", "plan-codex"].contains(name) {
            guard let u = try? JSONDecoder().decode(Usage.self, from: Data(json.utf8)) else { print("decode failed: \(name)"); continue }
            let view = Dashboard(u: u).background(Color(nsColor: .windowBackgroundColor))
            write(png(view, size: NSHostingView(rootView: view).fittingSize), dir, "menu-\(name)")
        }
    }

    /// The menu bar item in each state, on a light and a dark menu bar, at 3x.
    static func bars(to dir: URL) {
        let rows = ["waiting", "working", "working", "done"].map {
            Row(agent: "claude", who: "Claude Code", status: $0, label: $0, name: "x", cwd: "", updated: 0, detail: nil, session_id: nil)
        }
        let many = (0..<8).map { i in Row(agent: "codex", who: "Codex", status: i % 3 == 0 ? "waiting" : "working", label: "", name: "x", cwd: "", updated: 0, detail: nil, session_id: nil) }
        let fx = fixtures()
        func u(_ k: String) -> Usage? { fx[k].flatMap { try? JSONDecoder().decode(Usage.self, from: Data($0.utf8)) } }
        let states: [(String, [Row], Usage?)] = [
            ("bar-idle", [], nil), ("bar-sessions", rows, nil), ("bar-many", many, nil),
            ("bar-plan-ok", rows, u("plan-ok")), ("bar-plan-warn", rows, u("plan-warn")), ("bar-plan-blocked", [], u("plan-blocked")),
            ("bar-plan-credits", rows, u("plan-credits")), ("bar-api-warn", rows, u("api-warn")), ("bar-api-over", rows, u("api-over")),
            ("bar-api-pass", rows, u("api-pass")), ("bar-plan-codex-warn", rows, u("plan-codex")), ("bar-plan-claude-closest", rows, u("plan-claude-closest")),
        ]
        for (name, rs, us) in states {
            for dark in [false, true] {
                let fg = dark ? NSColor(white: 1, alpha: 0.92) : NSColor(white: 0, alpha: 0.85)
                // Two items, as in the menu bar: the dots, then (with Usage on) the gate and figure.
                let dots = StatusArt.image(rows: rs, usage: nil, fg: fg)
                let gate = StatusArt.gateImage(us, fg: fg)
                let text = StatusArt.attributedTitle(us, fg: fg)
                let itemGap: CGFloat = 16   // macOS's padding between two menu bar items
                let w = dots.size.width + (gate.map { itemGap + $0.size.width + text.size().width } ?? 0) + 16, h: CGFloat = 24, k: CGFloat = 3
                let out = NSImage(size: NSSize(width: w * k, height: h * k), flipped: false) { r in
                    (dark ? NSColor(white: 0.13, alpha: 1) : NSColor(white: 0.93, alpha: 1)).setFill(); r.fill()
                    NSGraphicsContext.current?.cgContext.scaleBy(x: k, y: k)
                    dots.draw(in: NSRect(x: 8, y: (h - 18) / 2, width: dots.size.width, height: 18))
                    if let gate {
                        let x = 8 + dots.size.width + itemGap
                        gate.draw(in: NSRect(x: x, y: (h - 18) / 2, width: gate.size.width, height: 18))
                        text.draw(at: NSPoint(x: x + gate.size.width, y: (h - text.size().height) / 2))
                    }
                    return true
                }
                if let t = out.tiffRepresentation, let rep = NSBitmapImageRep(data: t), let p = rep.representation(using: .png, properties: [:]) {
                    try? p.write(to: dir.appendingPathComponent("\(name)-\(dark ? "dark" : "light").png"))
                }
            }
        }
    }

    static func iconset(to dir: URL) {
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let tile = ZStack {
            RoundedRectangle(cornerRadius: 230, style: .continuous).fill(night)
            SignalArt(fixed: 2).padding(.vertical, 120)
        }
        .padding(90)
        guard let data = png(tile, size: CGSize(width: 1024, height: 1024)), let image = NSImage(data: data) else { return }
        for (px, name) in [(16, "16x16"), (32, "16x16@2x"), (32, "32x32"), (64, "32x32@2x"), (128, "128x128"),
                           (256, "128x128@2x"), (256, "256x256"), (512, "256x256@2x"), (512, "512x512"), (1024, "512x512@2x")] {
            let out = NSImage(size: NSSize(width: px, height: px))
            out.lockFocus(); image.draw(in: NSRect(x: 0, y: 0, width: px, height: px)); out.unlockFocus()
            if let t = out.tiffRepresentation, let rep = NSBitmapImageRep(data: t), let p = rep.representation(using: .png, properties: [:]) {
                try? p.write(to: dir.appendingPathComponent("icon_\(name).png"))
            }
        }
    }
}

// MARK: - App

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    var bar: MenuBar?
    var setup = Setup()
    var window: NSWindow?

    func applicationWillFinishLaunching(_ note: Notification) {
        // signals://settings and signals://refresh reach the running app.
        NSAppleEventManager.shared().setEventHandler(self, andSelector: #selector(handleURL(_:reply:)),
                                                     forEventClass: AEEventClass(kInternetEventClass), andEventID: AEEventID(kAEGetURL))
    }

    func applicationDidFinishLaunching(_ note: Notification) {
        let args = CommandLine.arguments
        if let i = args.firstIndex(of: "--render"), i + 1 < args.count {
            Renderer.review(to: URL(fileURLWithPath: args[i + 1])); NSApp.terminate(nil); return
        }
        if let i = args.firstIndex(of: "--icon"), i + 1 < args.count {
            Renderer.iconset(to: URL(fileURLWithPath: args[i + 1])); NSApp.terminate(nil); return
        }
        // One copy only: if another is already in the menu bar, leave it be.
        let mine = NSRunningApplication.current
        if NSRunningApplication.runningApplications(withBundleIdentifier: Bundle.main.bundleIdentifier ?? "")
            .contains(where: { $0.processIdentifier != mine.processIdentifier }) {
            NSApp.terminate(nil); return
        }
        UNUserNotificationCenter.current().delegate = self
        bar = MenuBar(openSettings: { [weak self] page in self?.showWindow(welcome: false, page: page) })
        if args.contains("--welcome") { showWindow(welcome: true) } else if args.contains("--settings") { showWindow(welcome: false) }
    }

    // Show notifications even while the menu is open.
    nonisolated func userNotificationCenter(_ c: UNUserNotificationCenter, willPresent n: UNNotification,
                                            withCompletionHandler done: @escaping (UNNotificationPresentationOptions) -> Void) {
        done([.banner, .sound])
    }

    @objc func handleURL(_ event: NSAppleEventDescriptor, reply: NSAppleEventDescriptor) {
        guard let s = event.paramDescriptor(forKeyword: AEKeyword(keyDirectObject))?.stringValue, let url = URL(string: s),
              let host = url.host else { return }
        if host == "refresh" { bar?.refreshUsage(); bar?.refreshSessions(); return }
        guard host == "settings" else { return }
        let query = url.query ?? ""
        showWindow(welcome: query.contains("welcome=1"), page: query.contains("page=usage") ? "usage" : nil)
    }

    func showWindow(welcome: Bool, page: String? = nil) {
        if window == nil || !(window?.isVisible ?? false) {
            setup = Setup()
            setup.welcome = welcome
            setup.page = welcome ? .welcome : (page == "usage" ? .usage : .sessions)
            let w = window ?? NSWindow(contentRect: NSRect(origin: .zero, size: windowSize),
                                       styleMask: [.titled, .closable, .fullSizeContentView], backing: .buffered, defer: false)
            w.titlebarAppearsTransparent = true; w.titleVisibility = .hidden; w.isMovableByWindowBackground = true
            w.title = "Signals"; w.isReleasedWhenClosed = false
            w.contentView = NSHostingView(rootView: SetupView(s: setup, close: { [weak self, weak w] in
                w?.close(); self?.bar?.refreshUsage(); self?.bar?.refreshSessions()
            }))
            w.center()
            window = w
        } else if !welcome, let page {
            setup.page = page == "usage" ? .usage : .sessions
        }
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    // Closing the window leaves Signals running in the menu bar.
    func applicationShouldTerminateAfterLastWindowClosed(_ app: NSApplication) -> Bool { false }
}

@main @MainActor
enum Main {
    static func main() {
        let app = NSApplication.shared
        let delegate = AppDelegate()
        app.delegate = delegate
        app.setActivationPolicy(.accessory)   // menu bar only: never in the Dock
        app.run()
    }
}

private extension String {
    /// "opt/x" -> "/opt/x"
    var withRootSlash: String { hasPrefix("/") ? self : "/" + self }
}
