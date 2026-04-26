export namespace guiapp {
	
	export class RuntimeConfig {
	    apiBaseURL: string;
	    bearerToken: string;
	    nativeBrowseEnabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RuntimeConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.apiBaseURL = source["apiBaseURL"];
	        this.bearerToken = source["bearerToken"];
	        this.nativeBrowseEnabled = source["nativeBrowseEnabled"];
	    }
	}

}

