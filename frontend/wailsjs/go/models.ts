export namespace engine {
	
	export class CellInput {
	    kind: string;
	    beast: string;
	    tier: number;
	    event: string;
	    known: number;
	    atLeast: number;
	    scratch: boolean;
	    ivory: boolean;
	    nestDue: number;
	
	    static createFrom(source: any = {}) {
	        return new CellInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.beast = source["beast"];
	        this.tier = source["tier"];
	        this.event = source["event"];
	        this.known = source["known"];
	        this.atLeast = source["atLeast"];
	        this.scratch = source["scratch"];
	        this.ivory = source["ivory"];
	        this.nestDue = source["nestDue"];
	    }
	}
	export class BoardInput {
	    cells: CellInput[];
	    warden: number;
	    day: number;
	    maxDays: number;
	    phase: string;
	    dest: number[];
	    tools: string[];
	    vault: number;
	    objective: string;
	    progress: number;
	    milestone: boolean;
	    startEvent: string;
	    chain: boolean;
	    chainSelect: boolean;
	    chainTargets: number[];
	    usedSwap: boolean;
	    usedDevour: boolean;
	    noSwap: boolean;
	    noDevour: boolean;
	    devourAfterSwap: boolean;
	    eventKind: string;
	    pending: string;
	    offer: string[];
	    giftPicks: number;
	    rerolls: number;
	    freshOffer: boolean;
	
	    static createFrom(source: any = {}) {
	        return new BoardInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.cells = this.convertValues(source["cells"], CellInput);
	        this.warden = source["warden"];
	        this.day = source["day"];
	        this.maxDays = source["maxDays"];
	        this.phase = source["phase"];
	        this.dest = source["dest"];
	        this.tools = source["tools"];
	        this.vault = source["vault"];
	        this.objective = source["objective"];
	        this.progress = source["progress"];
	        this.milestone = source["milestone"];
	        this.startEvent = source["startEvent"];
	        this.chain = source["chain"];
	        this.chainSelect = source["chainSelect"];
	        this.chainTargets = source["chainTargets"];
	        this.usedSwap = source["usedSwap"];
	        this.usedDevour = source["usedDevour"];
	        this.noSwap = source["noSwap"];
	        this.noDevour = source["noDevour"];
	        this.devourAfterSwap = source["devourAfterSwap"];
	        this.eventKind = source["eventKind"];
	        this.pending = source["pending"];
	        this.offer = source["offer"];
	        this.giftPicks = source["giftPicks"];
	        this.rerolls = source["rerolls"];
	        this.freshOffer = source["freshOffer"];
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
	
	export class Advice {
	    error: string;
	    jobId: number;
	    label: string;
	    why: string;
	    action: solver.Action;
	
	    static createFrom(source: any = {}) {
	        return new Advice(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.error = source["error"];
	        this.jobId = source["jobId"];
	        this.label = source["label"];
	        this.why = source["why"];
	        this.action = this.convertValues(source["action"], solver.Action);
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
	export class GameStatus {
	    running: boolean;
	    foreground: boolean;
	
	    static createFrom(source: any = {}) {
	        return new GameStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.foreground = source["foreground"];
	    }
	}
	export class LogStatus {
	    path: string;
	    source: string;
	    state: string;
	    message: string;
	    bytes: number;
	    lines: number;
	    triggers: number;
	    lastTrigger: string;
	
	    static createFrom(source: any = {}) {
	        return new LogStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.source = source["source"];
	        this.state = source["state"];
	        this.message = source["message"];
	        this.bytes = source["bytes"];
	        this.lines = source["lines"];
	        this.triggers = source["triggers"];
	        this.lastTrigger = source["lastTrigger"];
	    }
	}
	export class Named {
	    id: string;
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new Named(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	    }
	}
	export class Meta {
	    tools: Named[];
	    events: Named[];
	    gifts: Named[];
	    tiers: string[];
	    beasts: Named[];
	    objectives: Named[];
	    startCards: Named[];
	
	    static createFrom(source: any = {}) {
	        return new Meta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tools = this.convertValues(source["tools"], Named);
	        this.events = this.convertValues(source["events"], Named);
	        this.gifts = this.convertValues(source["gifts"], Named);
	        this.tiers = source["tiers"];
	        this.beasts = this.convertValues(source["beasts"], Named);
	        this.objectives = this.convertValues(source["objectives"], Named);
	        this.startCards = this.convertValues(source["startCards"], Named);
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
	
	export class Settings {
	    overlayOn: boolean;
	    overlayX: number;
	    overlayY: number;
	    overlayWidth: number;
	    overlayOpacity: number;
	    hideFromCapture: boolean;
	    clickThrough: boolean;
	    collapsed: boolean;
	    cpu: string;
	    autoScan: boolean;
	    logPath: string;
	    gameFirst: boolean;
	    mode: string;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.overlayOn = source["overlayOn"];
	        this.overlayX = source["overlayX"];
	        this.overlayY = source["overlayY"];
	        this.overlayWidth = source["overlayWidth"];
	        this.overlayOpacity = source["overlayOpacity"];
	        this.hideFromCapture = source["hideFromCapture"];
	        this.clickThrough = source["clickThrough"];
	        this.collapsed = source["collapsed"];
	        this.cpu = source["cpu"];
	        this.autoScan = source["autoScan"];
	        this.logPath = source["logPath"];
	        this.gameFirst = source["gameFirst"];
	        this.mode = source["mode"];
	    }
	}

}

export namespace solver {
	
	export class Action {
	    type: number;
	    cell: number;
	    id: number;
	    accept: boolean;
	    choice: string;
	
	    static createFrom(source: any = {}) {
	        return new Action(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.cell = source["cell"];
	        this.id = source["id"];
	        this.accept = source["accept"];
	        this.choice = source["choice"];
	    }
	}

}

