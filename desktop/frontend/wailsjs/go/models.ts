export namespace agent {
	
	export class BackupIssue {
	    id: number;
	    job_id: number;
	    run_uuid: string;
	    path: string;
	    class: string;
	    severity: string;
	    detail: string;
	    retryable: boolean;
	    count: number;
	    first_seen: number;
	    last_seen: number;
	    reported: boolean;
	
	    static createFrom(source: any = {}) {
	        return new BackupIssue(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.job_id = source["job_id"];
	        this.run_uuid = source["run_uuid"];
	        this.path = source["path"];
	        this.class = source["class"];
	        this.severity = source["severity"];
	        this.detail = source["detail"];
	        this.retryable = source["retryable"];
	        this.count = source["count"];
	        this.first_seen = source["first_seen"];
	        this.last_seen = source["last_seen"];
	        this.reported = source["reported"];
	    }
	}
	export class BackupRun {
	    client_run_uuid: string;
	    job_id: number;
	    run_id: number;
	    trigger: string;
	    status: string;
	    stats: wire.RunProgress;
	    snapshot_id: number;
	    error?: string;
	    started_at: number;
	    finished_at: number;
	
	    static createFrom(source: any = {}) {
	        return new BackupRun(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.client_run_uuid = source["client_run_uuid"];
	        this.job_id = source["job_id"];
	        this.run_id = source["run_id"];
	        this.trigger = source["trigger"];
	        this.status = source["status"];
	        this.stats = this.convertValues(source["stats"], wire.RunProgress);
	        this.snapshot_id = source["snapshot_id"];
	        this.error = source["error"];
	        this.started_at = source["started_at"];
	        this.finished_at = source["finished_at"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class BackupJob {
	    id: number;
	    device_id: number;
	    repo_id: number;
	    name: string;
	    kind: string;
	    mode: string;
	    // Go type: wire
	    spec: any;
	    schedule: wire.JobSchedule;
	    retention_policy_id?: number;
	    priority: number;
	    enabled: boolean;
	    revision: number;
	    profile_sid: string;
	    required_modules: string[];
	    state: string;
	    created_at: number;
	    updated_at: number;
	    effective_state: string;
	    suspended_by_mode: boolean;
	    running: boolean;
	    last_run?: BackupRun;
	
	    static createFrom(source: any = {}) {
	        return new BackupJob(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.device_id = source["device_id"];
	        this.repo_id = source["repo_id"];
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.mode = source["mode"];
	        this.spec = this.convertValues(source["spec"], null);
	        this.schedule = this.convertValues(source["schedule"], wire.JobSchedule);
	        this.retention_policy_id = source["retention_policy_id"];
	        this.priority = source["priority"];
	        this.enabled = source["enabled"];
	        this.revision = source["revision"];
	        this.profile_sid = source["profile_sid"];
	        this.required_modules = source["required_modules"];
	        this.state = source["state"];
	        this.created_at = source["created_at"];
	        this.updated_at = source["updated_at"];
	        this.effective_state = source["effective_state"];
	        this.suspended_by_mode = source["suspended_by_mode"];
	        this.running = source["running"];
	        this.last_run = this.convertValues(source["last_run"], BackupRun);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ReadinessItem {
	    code: string;
	    severity: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new ReadinessItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.severity = source["severity"];
	        this.message = source["message"];
	    }
	}
	export class KeyStatus {
	    available: boolean;
	    error?: string;
	    connected: boolean;
	    pin_state: string;
	    pinned_fingerprint: string;
	    server_fingerprint: string;
	    repo_state: string;
	    repo_id?: number;
	    repo_uuid?: string;
	    escrow_mode?: string;
	    recovery_confirmed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new KeyStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.error = source["error"];
	        this.connected = source["connected"];
	        this.pin_state = source["pin_state"];
	        this.pinned_fingerprint = source["pinned_fingerprint"];
	        this.server_fingerprint = source["server_fingerprint"];
	        this.repo_state = source["repo_state"];
	        this.repo_id = source["repo_id"];
	        this.repo_uuid = source["repo_uuid"];
	        this.escrow_mode = source["escrow_mode"];
	        this.recovery_confirmed = source["recovery_confirmed"];
	    }
	}
	export class BackupReadiness {
	    ready: boolean;
	    keys: KeyStatus;
	    jobs: number;
	    runnable_jobs: number;
	    running: number;
	    last_run?: BackupRun;
	    last_success_at: number;
	    open_issues: number;
	    items: ReadinessItem[];
	
	    static createFrom(source: any = {}) {
	        return new BackupReadiness(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ready = source["ready"];
	        this.keys = this.convertValues(source["keys"], KeyStatus);
	        this.jobs = source["jobs"];
	        this.runnable_jobs = source["runnable_jobs"];
	        this.running = source["running"];
	        this.last_run = this.convertValues(source["last_run"], BackupRun);
	        this.last_success_at = source["last_success_at"];
	        this.open_issues = source["open_issues"];
	        this.items = this.convertValues(source["items"], ReadinessItem);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class ServerInfo {
	    version: string;
	    site_name: string;
	    quota: number;
	    used: number;
	    admin: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ServerInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.site_name = source["site_name"];
	        this.quota = source["quota"];
	        this.used = source["used"];
	        this.admin = source["admin"];
	    }
	}
	export class EnrollView {
	    status: string;
	    user_code: string;
	    expires_at: number;
	    key_fingerprint: string;
	    device_name: string;
	    username: string;
	    server_url: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new EnrollView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.user_code = source["user_code"];
	        this.expires_at = source["expires_at"];
	        this.key_fingerprint = source["key_fingerprint"];
	        this.device_name = source["device_name"];
	        this.username = source["username"];
	        this.server_url = source["server_url"];
	        this.error = source["error"];
	    }
	}
	export class Bootstrap {
	    version: string;
	    protocol_version: number;
	    connected: boolean;
	    configured: boolean;
	    revoked: boolean;
	    enrollment?: EnrollView;
	    settings: config.Settings;
	    device_name: string;
	    os: string;
	    user: string;
	    server_info?: ServerInfo;
	
	    static createFrom(source: any = {}) {
	        return new Bootstrap(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.protocol_version = source["protocol_version"];
	        this.connected = source["connected"];
	        this.configured = source["configured"];
	        this.revoked = source["revoked"];
	        this.enrollment = this.convertValues(source["enrollment"], EnrollView);
	        this.settings = this.convertValues(source["settings"], config.Settings);
	        this.device_name = source["device_name"];
	        this.os = source["os"];
	        this.user = source["user"];
	        this.server_info = this.convertValues(source["server_info"], ServerInfo);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ConnectRequest {
	    url: string;
	    login: string;
	    password: string;
	    pin?: string;
	
	    static createFrom(source: any = {}) {
	        return new ConnectRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.login = source["login"];
	        this.password = source["password"];
	        this.pin = source["pin"];
	    }
	}
	
	export class InitRepositoryResult {
	    repo_id: number;
	    repo_uuid: string;
	    escrow_mode: string;
	    recovery_key: string;
	
	    static createFrom(source: any = {}) {
	        return new InitRepositoryResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.repo_id = source["repo_id"];
	        this.repo_uuid = source["repo_uuid"];
	        this.escrow_mode = source["escrow_mode"];
	        this.recovery_key = source["recovery_key"];
	    }
	}
	export class IssueQuery {
	    job_id?: number;
	    run_uuid?: string;
	
	    static createFrom(source: any = {}) {
	        return new IssueQuery(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.job_id = source["job_id"];
	        this.run_uuid = source["run_uuid"];
	    }
	}
	
	export class PreviewResult {
	    entries: engine.PreviewEntry[];
	    total: number;
	
	    static createFrom(source: any = {}) {
	        return new PreviewResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.entries = this.convertValues(source["entries"], engine.PreviewEntry);
	        this.total = source["total"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ProbeRequest {
	    url: string;
	    pin?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProbeRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.pin = source["pin"];
	    }
	}
	export class ProbeResult {
	    ok: boolean;
	    info?: Record<string, any>;
	    untrusted: boolean;
	    fingerprint?: string;
	    subject?: string;
	    reason?: string;
	    url: string;
	
	    static createFrom(source: any = {}) {
	        return new ProbeResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.info = source["info"];
	        this.untrusted = source["untrusted"];
	        this.fingerprint = source["fingerprint"];
	        this.subject = source["subject"];
	        this.reason = source["reason"];
	        this.url = source["url"];
	    }
	}
	

}

export namespace config {
	
	export class Settings {
	    server_url: string;
	    pinned_cert?: string;
	    device_id?: number;
	    device_name?: string;
	    username?: string;
	    key_fingerprint?: string;
	    auto_start: boolean;
	    start_minimized: boolean;
	    close_to_tray: boolean;
	    concurrency_mode: string;
	    manual_parallel: number;
	    max_parallel: number;
	    bandwidth_kbps: number;
	    notifications: boolean;
	    paused: boolean;
	    auto_update: boolean;
	    direct_upload: boolean;
	    direct_url?: string;
	    direct_reset_gen?: number;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.server_url = source["server_url"];
	        this.pinned_cert = source["pinned_cert"];
	        this.device_id = source["device_id"];
	        this.device_name = source["device_name"];
	        this.username = source["username"];
	        this.key_fingerprint = source["key_fingerprint"];
	        this.auto_start = source["auto_start"];
	        this.start_minimized = source["start_minimized"];
	        this.close_to_tray = source["close_to_tray"];
	        this.concurrency_mode = source["concurrency_mode"];
	        this.manual_parallel = source["manual_parallel"];
	        this.max_parallel = source["max_parallel"];
	        this.bandwidth_kbps = source["bandwidth_kbps"];
	        this.notifications = source["notifications"];
	        this.paused = source["paused"];
	        this.auto_update = source["auto_update"];
	        this.direct_upload = source["direct_upload"];
	        this.direct_url = source["direct_url"];
	        this.direct_reset_gen = source["direct_reset_gen"];
	    }
	}
	export class SyncConfig {
	    id: string;
	    name: string;
	    enabled: boolean;
	    owner_sid?: string;
	    kind: string;
	    local_path: string;
	    files?: string[];
	    destination: string;
	    recursive: boolean;
	    compress?: boolean;
	    include_subfolders?: string[];
	    exclude_subfolders?: string[];
	    include_extensions?: string[];
	    exclude_extensions?: string[];
	    min_size?: number;
	    max_size?: number;
	    include_patterns?: string[];
	    exclude_patterns?: string[];
	    modified_after?: string;
	    skip_hidden: boolean;
	    mode: string;
	    scan_interval_minutes: number;
	    stable_seconds: number;
	    automation_mode: string;
	    automations?: number[];
	    on_conflict: string;
	    created_at: number;
	    updated_at: number;
	
	    static createFrom(source: any = {}) {
	        return new SyncConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.enabled = source["enabled"];
	        this.owner_sid = source["owner_sid"];
	        this.kind = source["kind"];
	        this.local_path = source["local_path"];
	        this.files = source["files"];
	        this.destination = source["destination"];
	        this.recursive = source["recursive"];
	        this.compress = source["compress"];
	        this.include_subfolders = source["include_subfolders"];
	        this.exclude_subfolders = source["exclude_subfolders"];
	        this.include_extensions = source["include_extensions"];
	        this.exclude_extensions = source["exclude_extensions"];
	        this.min_size = source["min_size"];
	        this.max_size = source["max_size"];
	        this.include_patterns = source["include_patterns"];
	        this.exclude_patterns = source["exclude_patterns"];
	        this.modified_after = source["modified_after"];
	        this.skip_hidden = source["skip_hidden"];
	        this.mode = source["mode"];
	        this.scan_interval_minutes = source["scan_interval_minutes"];
	        this.stable_seconds = source["stable_seconds"];
	        this.automation_mode = source["automation_mode"];
	        this.automations = source["automations"];
	        this.on_conflict = source["on_conflict"];
	        this.created_at = source["created_at"];
	        this.updated_at = source["updated_at"];
	    }
	}

}

export namespace engine {
	
	export class ActiveView {
	    id: number;
	    config_id: string;
	    config: string;
	    path: string;
	    remote: string;
	    size: number;
	    sent: number;
	    speed: number;
	    phase: string;
	    started: number;
	    attempts: number;
	
	    static createFrom(source: any = {}) {
	        return new ActiveView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.config_id = source["config_id"];
	        this.config = source["config"];
	        this.path = source["path"];
	        this.remote = source["remote"];
	        this.size = source["size"];
	        this.sent = source["sent"];
	        this.speed = source["speed"];
	        this.phase = source["phase"];
	        this.started = source["started"];
	        this.attempts = source["attempts"];
	    }
	}
	export class AutomationView {
	    id: number;
	    name: string;
	    priority: number;
	    triggers: string[];
	    actions: string[];
	
	    static createFrom(source: any = {}) {
	        return new AutomationView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.priority = source["priority"];
	        this.triggers = source["triggers"];
	        this.actions = source["actions"];
	    }
	}
	export class ConfigView {
	    id: string;
	    name: string;
	    enabled: boolean;
	    owner_sid?: string;
	    kind: string;
	    local_path: string;
	    files?: string[];
	    destination: string;
	    recursive: boolean;
	    compress?: boolean;
	    include_subfolders?: string[];
	    exclude_subfolders?: string[];
	    include_extensions?: string[];
	    exclude_extensions?: string[];
	    min_size?: number;
	    max_size?: number;
	    include_patterns?: string[];
	    exclude_patterns?: string[];
	    modified_after?: string;
	    skip_hidden: boolean;
	    mode: string;
	    scan_interval_minutes: number;
	    stable_seconds: number;
	    automation_mode: string;
	    automations?: number[];
	    on_conflict: string;
	    created_at: number;
	    updated_at: number;
	    files_synced: number;
	    files_pending: number;
	    files_failed: number;
	    last_scan_at: number;
	    scanning: boolean;
	    last_error: string;
	    watching: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ConfigView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.enabled = source["enabled"];
	        this.owner_sid = source["owner_sid"];
	        this.kind = source["kind"];
	        this.local_path = source["local_path"];
	        this.files = source["files"];
	        this.destination = source["destination"];
	        this.recursive = source["recursive"];
	        this.compress = source["compress"];
	        this.include_subfolders = source["include_subfolders"];
	        this.exclude_subfolders = source["exclude_subfolders"];
	        this.include_extensions = source["include_extensions"];
	        this.exclude_extensions = source["exclude_extensions"];
	        this.min_size = source["min_size"];
	        this.max_size = source["max_size"];
	        this.include_patterns = source["include_patterns"];
	        this.exclude_patterns = source["exclude_patterns"];
	        this.modified_after = source["modified_after"];
	        this.skip_hidden = source["skip_hidden"];
	        this.mode = source["mode"];
	        this.scan_interval_minutes = source["scan_interval_minutes"];
	        this.stable_seconds = source["stable_seconds"];
	        this.automation_mode = source["automation_mode"];
	        this.automations = source["automations"];
	        this.on_conflict = source["on_conflict"];
	        this.created_at = source["created_at"];
	        this.updated_at = source["updated_at"];
	        this.files_synced = source["files_synced"];
	        this.files_pending = source["files_pending"];
	        this.files_failed = source["files_failed"];
	        this.last_scan_at = source["last_scan_at"];
	        this.scanning = source["scanning"];
	        this.last_error = source["last_error"];
	        this.watching = source["watching"];
	    }
	}
	export class DirectView {
	    enabled: boolean;
	    active: boolean;
	    url: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new DirectView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.active = source["active"];
	        this.url = source["url"];
	        this.message = source["message"];
	    }
	}
	export class PreviewEntry {
	    rel_path: string;
	    size: number;
	    modified: number;
	    decision: string;
	    reason?: string;
	    remote?: string;
	    tags?: string[];
	
	    static createFrom(source: any = {}) {
	        return new PreviewEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rel_path = source["rel_path"];
	        this.size = source["size"];
	        this.modified = source["modified"];
	        this.decision = source["decision"];
	        this.reason = source["reason"];
	        this.remote = source["remote"];
	        this.tags = source["tags"];
	    }
	}
	export class Stats {
	    active: number;
	    queued: number;
	    failed: number;
	    pending_bytes: number;
	    speed_bps: number;
	    concurrency: number;
	    concurrency_limit: number;
	    eta_seconds: number;
	    session_completed: number;
	    session_bytes: number;
	    last_failed_at: number;
	    governor?: transfer.GovernorState;
	    verifying: number;
	
	    static createFrom(source: any = {}) {
	        return new Stats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.active = source["active"];
	        this.queued = source["queued"];
	        this.failed = source["failed"];
	        this.pending_bytes = source["pending_bytes"];
	        this.speed_bps = source["speed_bps"];
	        this.concurrency = source["concurrency"];
	        this.concurrency_limit = source["concurrency_limit"];
	        this.eta_seconds = source["eta_seconds"];
	        this.session_completed = source["session_completed"];
	        this.session_bytes = source["session_bytes"];
	        this.last_failed_at = source["last_failed_at"];
	        this.governor = this.convertValues(source["governor"], transfer.GovernorState);
	        this.verifying = source["verifying"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Snapshot {
	    status: string;
	    message: string;
	    paused: boolean;
	    connected: boolean;
	    stats: Stats;
	    active: ActiveView[];
	    automations: number;
	    direct: DirectView;
	
	    static createFrom(source: any = {}) {
	        return new Snapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.message = source["message"];
	        this.paused = source["paused"];
	        this.connected = source["connected"];
	        this.stats = this.convertValues(source["stats"], Stats);
	        this.active = this.convertValues(source["active"], ActiveView);
	        this.automations = source["automations"];
	        this.direct = this.convertValues(source["direct"], DirectView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace main {
	
	export class BackupJobDraft {
	    id: number;
	    revision: number;
	    name: string;
	    paths: string[];
	    exclude: string[];
	    schedule: wire.JobSchedule;
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new BackupJobDraft(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.revision = source["revision"];
	        this.name = source["name"];
	        this.paths = source["paths"];
	        this.exclude = source["exclude"];
	        this.schedule = this.convertValues(source["schedule"], wire.JobSchedule);
	        this.enabled = source["enabled"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ConsentStatus {
	    supported: boolean;
	    user: string;
	
	    static createFrom(source: any = {}) {
	        return new ConsentStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.supported = source["supported"];
	        this.user = source["user"];
	    }
	}
	export class Elevation {
	    admin: boolean;
	    elevated: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Elevation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.admin = source["admin"];
	        this.elevated = source["elevated"];
	    }
	}
	export class KnownFolder {
	    token: string;
	    path: string;
	    exists: boolean;
	
	    static createFrom(source: any = {}) {
	        return new KnownFolder(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.token = source["token"];
	        this.path = source["path"];
	        this.exists = source["exists"];
	    }
	}
	export class ModeView {
	    source: string;
	    install_mode: string;
	    modules: modules.ModuleInfo[];
	    pending_request?: modules.ModeRequest;
	
	    static createFrom(source: any = {}) {
	        return new ModeView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = source["source"];
	        this.install_mode = source["install_mode"];
	        this.modules = this.convertValues(source["modules"], modules.ModuleInfo);
	        this.pending_request = this.convertValues(source["pending_request"], modules.ModeRequest);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace modules {
	
	export class AffectedItem {
	    kind: string;
	    id: string;
	    name: string;
	    requires: string[];
	    reason?: string;
	
	    static createFrom(source: any = {}) {
	        return new AffectedItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.id = source["id"];
	        this.name = source["name"];
	        this.requires = source["requires"];
	        this.reason = source["reason"];
	    }
	}
	export class SwitchPreview {
	    from: string;
	    to: string;
	    enable?: string[];
	    want?: string[];
	    suspend?: string[];
	    affected?: AffectedItem[];
	    no_deletes: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SwitchPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = source["from"];
	        this.to = source["to"];
	        this.enable = source["enable"];
	        this.want = source["want"];
	        this.suspend = source["suspend"];
	        this.affected = this.convertValues(source["affected"], AffectedItem);
	        this.no_deletes = source["no_deletes"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ModeRequest {
	    id: string;
	    task_id: number;
	    current_mode: string;
	    target_mode: string;
	    requested_by: string;
	    received_at: number;
	    preview: SwitchPreview;
	
	    static createFrom(source: any = {}) {
	        return new ModeRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.task_id = source["task_id"];
	        this.current_mode = source["current_mode"];
	        this.target_mode = source["target_mode"];
	        this.requested_by = source["requested_by"];
	        this.received_at = source["received_at"];
	        this.preview = this.convertValues(source["preview"], SwitchPreview);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ModuleInfo {
	    id: string;
	    kind: string;
	    version: string;
	    state: string;
	    error?: string;
	    updated_at: number;
	
	    static createFrom(source: any = {}) {
	        return new ModuleInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.version = source["version"];
	        this.state = source["state"];
	        this.error = source["error"];
	        this.updated_at = source["updated_at"];
	    }
	}

}

export namespace state {
	
	export class HistoryEntry {
	    id: number;
	    config_id: string;
	    rel_path: string;
	    remote_path: string;
	    size: number;
	    status: string;
	    result: string;
	    error: string;
	    duration_ms: number;
	    finished_at: number;
	
	    static createFrom(source: any = {}) {
	        return new HistoryEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.config_id = source["config_id"];
	        this.rel_path = source["rel_path"];
	        this.remote_path = source["remote_path"];
	        this.size = source["size"];
	        this.status = source["status"];
	        this.result = source["result"];
	        this.error = source["error"];
	        this.duration_ms = source["duration_ms"];
	        this.finished_at = source["finished_at"];
	    }
	}
	export class Item {
	    id: number;
	    config_id: string;
	    rel_path: string;
	    event: string;
	    size: number;
	    mtime: number;
	    remote_dir: string;
	    remote_name: string;
	    tags: string[];
	    attempts: number;
	    next_at: number;
	    status: string;
	    error: string;
	    manual: boolean;
	    created_at: number;
	
	    static createFrom(source: any = {}) {
	        return new Item(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.config_id = source["config_id"];
	        this.rel_path = source["rel_path"];
	        this.event = source["event"];
	        this.size = source["size"];
	        this.mtime = source["mtime"];
	        this.remote_dir = source["remote_dir"];
	        this.remote_name = source["remote_name"];
	        this.tags = source["tags"];
	        this.attempts = source["attempts"];
	        this.next_at = source["next_at"];
	        this.status = source["status"];
	        this.error = source["error"];
	        this.manual = source["manual"];
	        this.created_at = source["created_at"];
	    }
	}

}

export namespace transfer {
	
	export class GovernorState {
	    ceiling: number;
	    hardware: number;
	    held: boolean;
	    heavy: boolean;
	    load: number;
	    reason?: string;
	
	    static createFrom(source: any = {}) {
	        return new GovernorState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ceiling = source["ceiling"];
	        this.hardware = source["hardware"];
	        this.held = source["held"];
	        this.heavy = source["heavy"];
	        this.load = source["load"];
	        this.reason = source["reason"];
	    }
	}

}

export namespace updater {
	
	export class Status {
	    state: string;
	    current: string;
	    latest?: string;
	    url?: string;
	    notes?: string;
	    progress: number;
	    error?: string;
	    checked_at?: number;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.current = source["current"];
	        this.latest = source["latest"];
	        this.url = source["url"];
	        this.notes = source["notes"];
	        this.progress = source["progress"];
	        this.error = source["error"];
	        this.checked_at = source["checked_at"];
	    }
	}

}

export namespace wire {
	
	export class JobSchedule {
	    type: string;
	    expr: string;
	    at: string;
	    weekdays: number[];
	    every_minutes: number;
	    timezone: string;
	    catch_up: string;
	    jitter_minutes: number;
	
	    static createFrom(source: any = {}) {
	        return new JobSchedule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.expr = source["expr"];
	        this.at = source["at"];
	        this.weekdays = source["weekdays"];
	        this.every_minutes = source["every_minutes"];
	        this.timezone = source["timezone"];
	        this.catch_up = source["catch_up"];
	        this.jitter_minutes = source["jitter_minutes"];
	    }
	}
	export class RunProgress {
	    status: string;
	    phase: string;
	    files_total: number;
	    files_done: number;
	    bytes_total: number;
	    bytes_done: number;
	    bytes_new: number;
	    bytes_uploaded: number;
	    queue_packs: number;
	    warnings: number;
	    errors: number;
	    eta_ms: number;
	
	    static createFrom(source: any = {}) {
	        return new RunProgress(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.phase = source["phase"];
	        this.files_total = source["files_total"];
	        this.files_done = source["files_done"];
	        this.bytes_total = source["bytes_total"];
	        this.bytes_done = source["bytes_done"];
	        this.bytes_new = source["bytes_new"];
	        this.bytes_uploaded = source["bytes_uploaded"];
	        this.queue_packs = source["queue_packs"];
	        this.warnings = source["warnings"];
	        this.errors = source["errors"];
	        this.eta_ms = source["eta_ms"];
	    }
	}

}

