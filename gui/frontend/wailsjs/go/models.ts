export namespace api {
	
	export class ImageHostFeedback {
	    Status: string;
	    SelectedHost: string;
	    AllowedHosts: string[];
	    Reuploaded: boolean;
	    Message: string;
	
	    static createFrom(source: any = {}) {
	        return new ImageHostFeedback(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Status = source["Status"];
	        this.SelectedHost = source["SelectedHost"];
	        this.AllowedHosts = source["AllowedHosts"];
	        this.Reuploaded = source["Reuploaded"];
	        this.Message = source["Message"];
	    }
	}
	export class DescriptionBuilderGroup {
	    GroupKey: string;
	    Trackers: string[];
	    RawDescription: string;
	    RawDescriptionHTML: string;
	    HasOverride: boolean;
	    ImageHost: ImageHostFeedback;
	
	    static createFrom(source: any = {}) {
	        return new DescriptionBuilderGroup(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.GroupKey = source["GroupKey"];
	        this.Trackers = source["Trackers"];
	        this.RawDescription = source["RawDescription"];
	        this.RawDescriptionHTML = source["RawDescriptionHTML"];
	        this.HasOverride = source["HasOverride"];
	        this.ImageHost = this.convertValues(source["ImageHost"], ImageHostFeedback);
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
	export class DescriptionBuilderPreview {
	    SourcePath: string;
	    Groups: DescriptionBuilderGroup[];
	
	    static createFrom(source: any = {}) {
	        return new DescriptionBuilderPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SourcePath = source["SourcePath"];
	        this.Groups = this.convertValues(source["Groups"], DescriptionBuilderGroup);
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
	export class DescriptionOverride {
	    SourcePath: string;
	    GroupKey: string;
	    Description: string;
	    // Go type: time
	    UpdatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new DescriptionOverride(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SourcePath = source["SourcePath"];
	        this.GroupKey = source["GroupKey"];
	        this.Description = source["Description"];
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
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
	export class DupeEpisodeMatch {
	    ID: string;
	    Name: string;
	    Link: string;
	    Tracker: string;
	    Internal: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DupeEpisodeMatch(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Name = source["Name"];
	        this.Link = source["Link"];
	        this.Tracker = source["Tracker"];
	        this.Internal = source["Internal"];
	    }
	}
	export class DupeMatch {
	    FilenameMatch: string;
	    FileCountMatch: number;
	    SizeMatch: string;
	    TrumpableID: string;
	    MatchedID: string;
	    MatchedName: string;
	    MatchedLink: string;
	    MatchedDownload: string;
	    MatchedReason: string;
	    SeasonPackExists: boolean;
	    SeasonPackName: string;
	    SeasonPackLink: string;
	    SeasonPackID: string;
	    SeasonPackContainsEpisode: boolean;
	    MatchedEpisodeIDs: DupeEpisodeMatch[];
	
	    static createFrom(source: any = {}) {
	        return new DupeMatch(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.FilenameMatch = source["FilenameMatch"];
	        this.FileCountMatch = source["FileCountMatch"];
	        this.SizeMatch = source["SizeMatch"];
	        this.TrumpableID = source["TrumpableID"];
	        this.MatchedID = source["MatchedID"];
	        this.MatchedName = source["MatchedName"];
	        this.MatchedLink = source["MatchedLink"];
	        this.MatchedDownload = source["MatchedDownload"];
	        this.MatchedReason = source["MatchedReason"];
	        this.SeasonPackExists = source["SeasonPackExists"];
	        this.SeasonPackName = source["SeasonPackName"];
	        this.SeasonPackLink = source["SeasonPackLink"];
	        this.SeasonPackID = source["SeasonPackID"];
	        this.SeasonPackContainsEpisode = source["SeasonPackContainsEpisode"];
	        this.MatchedEpisodeIDs = this.convertValues(source["MatchedEpisodeIDs"], DupeEpisodeMatch);
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
	export class DupeEntry {
	    Name: string;
	    SizeBytes: number;
	    SizeKnown: boolean;
	    SizeText: string;
	    Files: string[];
	    FileCount: number;
	    Trumpable: boolean;
	    Link: string;
	    Download: string;
	    Flags: string[];
	    ID: string;
	    Type: string;
	    Res: string;
	    Internal: boolean;
	    BDInfo: string;
	    Description: string;
	
	    static createFrom(source: any = {}) {
	        return new DupeEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Name = source["Name"];
	        this.SizeBytes = source["SizeBytes"];
	        this.SizeKnown = source["SizeKnown"];
	        this.SizeText = source["SizeText"];
	        this.Files = source["Files"];
	        this.FileCount = source["FileCount"];
	        this.Trumpable = source["Trumpable"];
	        this.Link = source["Link"];
	        this.Download = source["Download"];
	        this.Flags = source["Flags"];
	        this.ID = source["ID"];
	        this.Type = source["Type"];
	        this.Res = source["Res"];
	        this.Internal = source["Internal"];
	        this.BDInfo = source["BDInfo"];
	        this.Description = source["Description"];
	    }
	}
	export class DupeCheckResult {
	    Tracker: string;
	    Raw: DupeEntry[];
	    Filtered: DupeEntry[];
	    HasDupes: boolean;
	    ContentFail: boolean;
	    Match: DupeMatch;
	    Notes: string[];
	    Skipped: boolean;
	    SkipReason: string;
	    SkipRules: string[];
	    Status: string;
	    Error: string;
	    // Go type: time
	    CheckedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new DupeCheckResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Tracker = source["Tracker"];
	        this.Raw = this.convertValues(source["Raw"], DupeEntry);
	        this.Filtered = this.convertValues(source["Filtered"], DupeEntry);
	        this.HasDupes = source["HasDupes"];
	        this.ContentFail = source["ContentFail"];
	        this.Match = this.convertValues(source["Match"], DupeMatch);
	        this.Notes = source["Notes"];
	        this.Skipped = source["Skipped"];
	        this.SkipReason = source["SkipReason"];
	        this.SkipRules = source["SkipRules"];
	        this.Status = source["Status"];
	        this.Error = source["Error"];
	        this.CheckedAt = this.convertValues(source["CheckedAt"], null);
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
	export class DupeCheckSummary {
	    SourcePath: string;
	    Results: DupeCheckResult[];
	    Notes: string[];
	
	    static createFrom(source: any = {}) {
	        return new DupeCheckSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SourcePath = source["SourcePath"];
	        this.Results = this.convertValues(source["Results"], DupeCheckResult);
	        this.Notes = source["Notes"];
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
	
	
	
	export class ExternalIDCandidate {
	    Provider: string;
	    ID: number;
	    Title: string;
	    OriginalTitle: string;
	    Year: number;
	    Category: string;
	    MediaType: string;
	    Overview: string;
	    PosterURL: string;
	    Similarity: number;
	
	    static createFrom(source: any = {}) {
	        return new ExternalIDCandidate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Provider = source["Provider"];
	        this.ID = source["ID"];
	        this.Title = source["Title"];
	        this.OriginalTitle = source["OriginalTitle"];
	        this.Year = source["Year"];
	        this.Category = source["Category"];
	        this.MediaType = source["MediaType"];
	        this.Overview = source["Overview"];
	        this.PosterURL = source["PosterURL"];
	        this.Similarity = source["Similarity"];
	    }
	}
	export class ExternalIDCandidates {
	    TMDB: ExternalIDCandidate[];
	    IMDB: ExternalIDCandidate[];
	    TMDBAutoSelected: boolean;
	    IMDBAutoSelected: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ExternalIDCandidates(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.TMDB = this.convertValues(source["TMDB"], ExternalIDCandidate);
	        this.IMDB = this.convertValues(source["IMDB"], ExternalIDCandidate);
	        this.TMDBAutoSelected = source["TMDBAutoSelected"];
	        this.IMDBAutoSelected = source["IMDBAutoSelected"];
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
	export class ExternalIDInfo {
	    Provider: string;
	    ID: number;
	    Source: string;
	
	    static createFrom(source: any = {}) {
	        return new ExternalIDInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Provider = source["Provider"];
	        this.ID = source["ID"];
	        this.Source = source["Source"];
	    }
	}
	export class ExternalIDOverrides {
	    TMDBID?: number;
	    IMDBID?: number;
	    TVDBID?: number;
	    TVmazeID?: number;
	    MALID?: number;
	
	    static createFrom(source: any = {}) {
	        return new ExternalIDOverrides(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.TMDBID = source["TMDBID"];
	        this.IMDBID = source["IMDBID"];
	        this.TVDBID = source["TVDBID"];
	        this.TVmazeID = source["TVmazeID"];
	        this.MALID = source["MALID"];
	    }
	}
	export class ExternalIDs {
	    SourcePath: string;
	    TMDBID: number;
	    IMDBID: number;
	    TVDBID: number;
	    TVmazeID: number;
	    Category: string;
	    SourceTMDB: string;
	    SourceIMDB: string;
	    SourceTVDB: string;
	    SourceTVmaze: string;
	    // Go type: time
	    UpdatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new ExternalIDs(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SourcePath = source["SourcePath"];
	        this.TMDBID = source["TMDBID"];
	        this.IMDBID = source["IMDBID"];
	        this.TVDBID = source["TVDBID"];
	        this.TVmazeID = source["TVmazeID"];
	        this.Category = source["Category"];
	        this.SourceTMDB = source["SourceTMDB"];
	        this.SourceIMDB = source["SourceIMDB"];
	        this.SourceTVDB = source["SourceTVDB"];
	        this.SourceTVmaze = source["SourceTVmaze"];
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
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
	export class TVmazeMetadata {
	    TVmazeID: number;
	    Name: string;
	    Premiered: string;
	    Ended: string;
	    Summary: string;
	    Status: string;
	    Type: string;
	    Language: string;
	    Genres: string;
	    Runtime: number;
	    AverageRuntime: number;
	    Rating: number;
	    Weight: number;
	    OfficialSite: string;
	    Country: string;
	    Network: string;
	    NetworkCountry: string;
	    NetworkLogo: string;
	    WebChannel: string;
	    WebCountry: string;
	    WebLogo: string;
	    Poster: string;
	    PosterMedium: string;
	    Backdrop: string;
	    BackdropMedium: string;
	    IMDBID: number;
	    TVDBID: number;
	
	    static createFrom(source: any = {}) {
	        return new TVmazeMetadata(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.TVmazeID = source["TVmazeID"];
	        this.Name = source["Name"];
	        this.Premiered = source["Premiered"];
	        this.Ended = source["Ended"];
	        this.Summary = source["Summary"];
	        this.Status = source["Status"];
	        this.Type = source["Type"];
	        this.Language = source["Language"];
	        this.Genres = source["Genres"];
	        this.Runtime = source["Runtime"];
	        this.AverageRuntime = source["AverageRuntime"];
	        this.Rating = source["Rating"];
	        this.Weight = source["Weight"];
	        this.OfficialSite = source["OfficialSite"];
	        this.Country = source["Country"];
	        this.Network = source["Network"];
	        this.NetworkCountry = source["NetworkCountry"];
	        this.NetworkLogo = source["NetworkLogo"];
	        this.WebChannel = source["WebChannel"];
	        this.WebCountry = source["WebCountry"];
	        this.WebLogo = source["WebLogo"];
	        this.Poster = source["Poster"];
	        this.PosterMedium = source["PosterMedium"];
	        this.Backdrop = source["Backdrop"];
	        this.BackdropMedium = source["BackdropMedium"];
	        this.IMDBID = source["IMDBID"];
	        this.TVDBID = source["TVDBID"];
	    }
	}
	export class TVDBMetadata {
	    TVDBID: number;
	    Name: string;
	    NameEnglish: string;
	    Overview: string;
	    OverviewEnglish: string;
	    FirstAired: string;
	    Year: number;
	    YearFromAlias: boolean;
	    Type: string;
	    Status: string;
	    Network: string;
	    OriginalCountry: string;
	    OriginalLanguage: string;
	    HasEnglish: boolean;
	    Genres: string;
	    Poster: string;
	    Aliases: string[];
	    EpisodeSeason: number;
	    EpisodeNumber: number;
	    EpisodeName: string;
	    EpisodeNameEnglish: string;
	    EpisodeOverview: string;
	    EpisodeOverviewEnglish: string;
	    EpisodeAired: string;
	
	    static createFrom(source: any = {}) {
	        return new TVDBMetadata(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.TVDBID = source["TVDBID"];
	        this.Name = source["Name"];
	        this.NameEnglish = source["NameEnglish"];
	        this.Overview = source["Overview"];
	        this.OverviewEnglish = source["OverviewEnglish"];
	        this.FirstAired = source["FirstAired"];
	        this.Year = source["Year"];
	        this.YearFromAlias = source["YearFromAlias"];
	        this.Type = source["Type"];
	        this.Status = source["Status"];
	        this.Network = source["Network"];
	        this.OriginalCountry = source["OriginalCountry"];
	        this.OriginalLanguage = source["OriginalLanguage"];
	        this.HasEnglish = source["HasEnglish"];
	        this.Genres = source["Genres"];
	        this.Poster = source["Poster"];
	        this.Aliases = source["Aliases"];
	        this.EpisodeSeason = source["EpisodeSeason"];
	        this.EpisodeNumber = source["EpisodeNumber"];
	        this.EpisodeName = source["EpisodeName"];
	        this.EpisodeNameEnglish = source["EpisodeNameEnglish"];
	        this.EpisodeOverview = source["EpisodeOverview"];
	        this.EpisodeOverviewEnglish = source["EpisodeOverviewEnglish"];
	        this.EpisodeAired = source["EpisodeAired"];
	    }
	}
	export class IMDBSeasonSummary {
	    Season: number;
	    Year: number;
	    YearRange: string;
	
	    static createFrom(source: any = {}) {
	        return new IMDBSeasonSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Season = source["Season"];
	        this.Year = source["Year"];
	        this.YearRange = source["YearRange"];
	    }
	}
	export class IMDBReleaseDate {
	    Year: number;
	    Month: number;
	    Day: number;
	
	    static createFrom(source: any = {}) {
	        return new IMDBReleaseDate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Year = source["Year"];
	        this.Month = source["Month"];
	        this.Day = source["Day"];
	    }
	}
	export class IMDBEpisode {
	    ID: string;
	    Title: string;
	    ReleaseYear: number;
	    ReleaseDate: IMDBReleaseDate;
	    Season: number;
	    EpisodeText: string;
	
	    static createFrom(source: any = {}) {
	        return new IMDBEpisode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Title = source["Title"];
	        this.ReleaseYear = source["ReleaseYear"];
	        this.ReleaseDate = this.convertValues(source["ReleaseDate"], IMDBReleaseDate);
	        this.Season = source["Season"];
	        this.EpisodeText = source["EpisodeText"];
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
	export class IMDBAKA {
	    Title: string;
	    Country: string;
	    Language: string;
	    Attributes: string[];
	
	    static createFrom(source: any = {}) {
	        return new IMDBAKA(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Title = source["Title"];
	        this.Country = source["Country"];
	        this.Language = source["Language"];
	        this.Attributes = source["Attributes"];
	    }
	}
	export class IMDBEditionDetail {
	    DisplayName: string;
	    Seconds: number;
	    Minutes: number;
	    Attributes: string[];
	
	    static createFrom(source: any = {}) {
	        return new IMDBEditionDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.DisplayName = source["DisplayName"];
	        this.Seconds = source["Seconds"];
	        this.Minutes = source["Minutes"];
	        this.Attributes = source["Attributes"];
	    }
	}
	export class IMDBPerson {
	    ID: string;
	    Name: string;
	
	    static createFrom(source: any = {}) {
	        return new IMDBPerson(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Name = source["Name"];
	    }
	}
	export class IMDBMetadata {
	    IMDBID: number;
	    IMDbIDText: string;
	    IMDbURL: string;
	    Title: string;
	    Year: number;
	    EndYear: number;
	    AKA: string;
	    Type: string;
	    Plot: string;
	    Rating: number;
	    RatingCount: number;
	    RatingText: string;
	    RuntimeMinutes: number;
	    RuntimeText: string;
	    Genres: string;
	    Country: string;
	    CountryList: string;
	    Cover: string;
	    Directors: IMDBPerson[];
	    Creators: IMDBPerson[];
	    Writers: IMDBPerson[];
	    Stars: IMDBPerson[];
	    Editions: string[];
	    EditionDetails: Record<string, IMDBEditionDetail>;
	    Akas: IMDBAKA[];
	    Episodes: IMDBEpisode[];
	    SeasonsSummary: IMDBSeasonSummary[];
	    SoundMixes: string[];
	    TVYear: number;
	    OriginalLanguage: string;
	
	    static createFrom(source: any = {}) {
	        return new IMDBMetadata(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.IMDBID = source["IMDBID"];
	        this.IMDbIDText = source["IMDbIDText"];
	        this.IMDbURL = source["IMDbURL"];
	        this.Title = source["Title"];
	        this.Year = source["Year"];
	        this.EndYear = source["EndYear"];
	        this.AKA = source["AKA"];
	        this.Type = source["Type"];
	        this.Plot = source["Plot"];
	        this.Rating = source["Rating"];
	        this.RatingCount = source["RatingCount"];
	        this.RatingText = source["RatingText"];
	        this.RuntimeMinutes = source["RuntimeMinutes"];
	        this.RuntimeText = source["RuntimeText"];
	        this.Genres = source["Genres"];
	        this.Country = source["Country"];
	        this.CountryList = source["CountryList"];
	        this.Cover = source["Cover"];
	        this.Directors = this.convertValues(source["Directors"], IMDBPerson);
	        this.Creators = this.convertValues(source["Creators"], IMDBPerson);
	        this.Writers = this.convertValues(source["Writers"], IMDBPerson);
	        this.Stars = this.convertValues(source["Stars"], IMDBPerson);
	        this.Editions = source["Editions"];
	        this.EditionDetails = this.convertValues(source["EditionDetails"], IMDBEditionDetail, true);
	        this.Akas = this.convertValues(source["Akas"], IMDBAKA);
	        this.Episodes = this.convertValues(source["Episodes"], IMDBEpisode);
	        this.SeasonsSummary = this.convertValues(source["SeasonsSummary"], IMDBSeasonSummary);
	        this.SoundMixes = source["SoundMixes"];
	        this.TVYear = source["TVYear"];
	        this.OriginalLanguage = source["OriginalLanguage"];
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
	export class TMDBNetwork {
	    ID: number;
	    Name: string;
	    LogoPath: string;
	    OriginCountry: string;
	
	    static createFrom(source: any = {}) {
	        return new TMDBNetwork(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Name = source["Name"];
	        this.LogoPath = source["LogoPath"];
	        this.OriginCountry = source["OriginCountry"];
	    }
	}
	export class TMDBCountry {
	    ISO3166: string;
	    Name: string;
	
	    static createFrom(source: any = {}) {
	        return new TMDBCountry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ISO3166 = source["ISO3166"];
	        this.Name = source["Name"];
	    }
	}
	export class TMDBCompany {
	    ID: number;
	    Name: string;
	    LogoPath: string;
	    OriginCountry: string;
	
	    static createFrom(source: any = {}) {
	        return new TMDBCompany(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Name = source["Name"];
	        this.LogoPath = source["LogoPath"];
	        this.OriginCountry = source["OriginCountry"];
	    }
	}
	export class TMDBMetadata {
	    TMDBID: number;
	    IMDBID: number;
	    TVDBID: number;
	    Category: string;
	    Title: string;
	    OriginalTitle: string;
	    Year: number;
	    ReleaseDate: string;
	    FirstAirDate: string;
	    LastAirDate: string;
	    OriginCountry: string[];
	    OriginalLanguage: string;
	    Overview: string;
	    Poster: string;
	    TMDBPosterPath: string;
	    Logo: string;
	    TMDBLogo: string;
	    Backdrop: string;
	    TMDBType: string;
	    Runtime: number;
	    Genres: string;
	    GenreIDs: string;
	    Creators: string[];
	    Directors: string[];
	    Cast: string[];
	    MALID: number;
	    Anime: boolean;
	    Demographic: string;
	    RetrievedAKA: string;
	    Keywords: string;
	    YouTube: string;
	    Certification: string;
	    ProductionCompanies: TMDBCompany[];
	    ProductionCountries: TMDBCountry[];
	    Networks: TMDBNetwork[];
	    IMDbMismatch: boolean;
	    MismatchedIMDbID: number;
	
	    static createFrom(source: any = {}) {
	        return new TMDBMetadata(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.TMDBID = source["TMDBID"];
	        this.IMDBID = source["IMDBID"];
	        this.TVDBID = source["TVDBID"];
	        this.Category = source["Category"];
	        this.Title = source["Title"];
	        this.OriginalTitle = source["OriginalTitle"];
	        this.Year = source["Year"];
	        this.ReleaseDate = source["ReleaseDate"];
	        this.FirstAirDate = source["FirstAirDate"];
	        this.LastAirDate = source["LastAirDate"];
	        this.OriginCountry = source["OriginCountry"];
	        this.OriginalLanguage = source["OriginalLanguage"];
	        this.Overview = source["Overview"];
	        this.Poster = source["Poster"];
	        this.TMDBPosterPath = source["TMDBPosterPath"];
	        this.Logo = source["Logo"];
	        this.TMDBLogo = source["TMDBLogo"];
	        this.Backdrop = source["Backdrop"];
	        this.TMDBType = source["TMDBType"];
	        this.Runtime = source["Runtime"];
	        this.Genres = source["Genres"];
	        this.GenreIDs = source["GenreIDs"];
	        this.Creators = source["Creators"];
	        this.Directors = source["Directors"];
	        this.Cast = source["Cast"];
	        this.MALID = source["MALID"];
	        this.Anime = source["Anime"];
	        this.Demographic = source["Demographic"];
	        this.RetrievedAKA = source["RetrievedAKA"];
	        this.Keywords = source["Keywords"];
	        this.YouTube = source["YouTube"];
	        this.Certification = source["Certification"];
	        this.ProductionCompanies = this.convertValues(source["ProductionCompanies"], TMDBCompany);
	        this.ProductionCountries = this.convertValues(source["ProductionCountries"], TMDBCountry);
	        this.Networks = this.convertValues(source["Networks"], TMDBNetwork);
	        this.IMDbMismatch = source["IMDbMismatch"];
	        this.MismatchedIMDbID = source["MismatchedIMDbID"];
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
	export class ExternalMetadata {
	    SourcePath: string;
	    TMDB?: TMDBMetadata;
	    IMDB?: IMDBMetadata;
	    TVDB?: TVDBMetadata;
	    TVmaze?: TVmazeMetadata;
	    // Go type: time
	    UpdatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new ExternalMetadata(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SourcePath = source["SourcePath"];
	        this.TMDB = this.convertValues(source["TMDB"], TMDBMetadata);
	        this.IMDB = this.convertValues(source["IMDB"], IMDBMetadata);
	        this.TVDB = this.convertValues(source["TVDB"], TVDBMetadata);
	        this.TVmaze = this.convertValues(source["TVmaze"], TVmazeMetadata);
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
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
	export class ExternalPreview {
	    Provider: string;
	    ID: number;
	    Source: string;
	    Title: string;
	    Year: number;
	    Overview: string;
	    PosterURL: string;
	    BackdropURL: string;
	    Category: string;
	    OriginalTitle: string;
	    ReleaseDate: string;
	    FirstAirDate: string;
	    LastAirDate: string;
	    OriginalLanguage: string;
	    TMDBType: string;
	    Runtime: number;
	    Genres: string;
	    Keywords: string;
	    YouTube: string;
	    IMDBType: string;
	    Rating: number;
	    RatingCount: number;
	    RuntimeMinutes: number;
	    Country: string;
	    Premiered: string;
	    IMDBID: number;
	    TVDBID: number;
	    TMDB?: TMDBMetadata;
	    IMDB?: IMDBMetadata;
	    TVDB?: TVDBMetadata;
	    TVmaze?: TVmazeMetadata;
	
	    static createFrom(source: any = {}) {
	        return new ExternalPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Provider = source["Provider"];
	        this.ID = source["ID"];
	        this.Source = source["Source"];
	        this.Title = source["Title"];
	        this.Year = source["Year"];
	        this.Overview = source["Overview"];
	        this.PosterURL = source["PosterURL"];
	        this.BackdropURL = source["BackdropURL"];
	        this.Category = source["Category"];
	        this.OriginalTitle = source["OriginalTitle"];
	        this.ReleaseDate = source["ReleaseDate"];
	        this.FirstAirDate = source["FirstAirDate"];
	        this.LastAirDate = source["LastAirDate"];
	        this.OriginalLanguage = source["OriginalLanguage"];
	        this.TMDBType = source["TMDBType"];
	        this.Runtime = source["Runtime"];
	        this.Genres = source["Genres"];
	        this.Keywords = source["Keywords"];
	        this.YouTube = source["YouTube"];
	        this.IMDBType = source["IMDBType"];
	        this.Rating = source["Rating"];
	        this.RatingCount = source["RatingCount"];
	        this.RuntimeMinutes = source["RuntimeMinutes"];
	        this.Country = source["Country"];
	        this.Premiered = source["Premiered"];
	        this.IMDBID = source["IMDBID"];
	        this.TVDBID = source["TVDBID"];
	        this.TMDB = this.convertValues(source["TMDB"], TMDBMetadata);
	        this.IMDB = this.convertValues(source["IMDB"], IMDBMetadata);
	        this.TVDB = this.convertValues(source["TVDB"], TVDBMetadata);
	        this.TVmaze = this.convertValues(source["TVmaze"], TVmazeMetadata);
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
	export class FileMetadata {
	    Path: string;
	    InfoHash: string;
	    // Go type: time
	    UpdatedAt: any;
	    DiscType: string;
	    VideoPath: string;
	    FileList: string[];
	    SourceSize: number;
	    Scene: boolean;
	    SceneName: string;
	    SceneIMDB: number;
	    Type: string;
	    Artist: string;
	    Title: string;
	    Subtitle: string;
	    Alt: string;
	    Year: number;
	    Month: number;
	    Day: number;
	    Source: string;
	    Resolution: string;
	    Codec: string[];
	    Audio: string[];
	    HDR: string[];
	    Ext: string;
	    Language: string[];
	    Site: string;
	    Genre: string;
	    Channels: string;
	    Collection: string;
	    Region: string;
	    Size: string;
	    Group: string;
	    Disc: string;
	    Edition: string[];
	    Other: string[];
	
	    static createFrom(source: any = {}) {
	        return new FileMetadata(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Path = source["Path"];
	        this.InfoHash = source["InfoHash"];
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
	        this.DiscType = source["DiscType"];
	        this.VideoPath = source["VideoPath"];
	        this.FileList = source["FileList"];
	        this.SourceSize = source["SourceSize"];
	        this.Scene = source["Scene"];
	        this.SceneName = source["SceneName"];
	        this.SceneIMDB = source["SceneIMDB"];
	        this.Type = source["Type"];
	        this.Artist = source["Artist"];
	        this.Title = source["Title"];
	        this.Subtitle = source["Subtitle"];
	        this.Alt = source["Alt"];
	        this.Year = source["Year"];
	        this.Month = source["Month"];
	        this.Day = source["Day"];
	        this.Source = source["Source"];
	        this.Resolution = source["Resolution"];
	        this.Codec = source["Codec"];
	        this.Audio = source["Audio"];
	        this.HDR = source["HDR"];
	        this.Ext = source["Ext"];
	        this.Language = source["Language"];
	        this.Site = source["Site"];
	        this.Genre = source["Genre"];
	        this.Channels = source["Channels"];
	        this.Collection = source["Collection"];
	        this.Region = source["Region"];
	        this.Size = source["Size"];
	        this.Group = source["Group"];
	        this.Disc = source["Disc"];
	        this.Edition = source["Edition"];
	        this.Other = source["Other"];
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
	export class HistoryEntry {
	    SourcePath: string;
	    ReleaseTitle: string;
	    ReleaseSource: string;
	    ReleaseResolution: string;
	    // Go type: time
	    MetadataUpdatedAt: any;
	    LatestUploadStatus: string;
	    // Go type: time
	    LatestUploadAt: any;
	    RuleFailureCount: number;
	
	    static createFrom(source: any = {}) {
	        return new HistoryEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SourcePath = source["SourcePath"];
	        this.ReleaseTitle = source["ReleaseTitle"];
	        this.ReleaseSource = source["ReleaseSource"];
	        this.ReleaseResolution = source["ReleaseResolution"];
	        this.MetadataUpdatedAt = this.convertValues(source["MetadataUpdatedAt"], null);
	        this.LatestUploadStatus = source["LatestUploadStatus"];
	        this.LatestUploadAt = this.convertValues(source["LatestUploadAt"], null);
	        this.RuleFailureCount = source["RuleFailureCount"];
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
	export class UploadRecord {
	    Tracker: string;
	    Status: string;
	    // Go type: time
	    CreatedAt: any;
	    SourcePath: string;
	
	    static createFrom(source: any = {}) {
	        return new UploadRecord(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Tracker = source["Tracker"];
	        this.Status = source["Status"];
	        this.CreatedAt = this.convertValues(source["CreatedAt"], null);
	        this.SourcePath = source["SourcePath"];
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
	export class UploadedImageLink {
	    SourcePath: string;
	    ImagePath: string;
	    Host: string;
	    UsageScope: string;
	    ImgURL: string;
	    RawURL: string;
	    WebURL: string;
	    SizeBytes: number;
	    // Go type: time
	    UploadedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new UploadedImageLink(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SourcePath = source["SourcePath"];
	        this.ImagePath = source["ImagePath"];
	        this.Host = source["Host"];
	        this.UsageScope = source["UsageScope"];
	        this.ImgURL = source["ImgURL"];
	        this.RawURL = source["RawURL"];
	        this.WebURL = source["WebURL"];
	        this.SizeBytes = source["SizeBytes"];
	        this.UploadedAt = this.convertValues(source["UploadedAt"], null);
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
	export class ScreenshotFinalSelection {
	    SourcePath: string;
	    ImagePath: string;
	    Order: number;
	    Source: string;
	    // Go type: time
	    SelectedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new ScreenshotFinalSelection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SourcePath = source["SourcePath"];
	        this.ImagePath = source["ImagePath"];
	        this.Order = source["Order"];
	        this.Source = source["Source"];
	        this.SelectedAt = this.convertValues(source["SelectedAt"], null);
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
	export class Screenshot {
	    SourcePath: string;
	    ImagePath: string;
	    Timestamp: number;
	    FrameNumber: number;
	    Width: number;
	    Height: number;
	    Purpose: string;
	    // Go type: time
	    CapturedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Screenshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SourcePath = source["SourcePath"];
	        this.ImagePath = source["ImagePath"];
	        this.Timestamp = source["Timestamp"];
	        this.FrameNumber = source["FrameNumber"];
	        this.Width = source["Width"];
	        this.Height = source["Height"];
	        this.Purpose = source["Purpose"];
	        this.CapturedAt = this.convertValues(source["CapturedAt"], null);
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
	export class TrackerRuleFailure {
	    SourcePath: string;
	    Tracker: string;
	    Rule: string;
	    Reason: string;
	    // Go type: time
	    CreatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new TrackerRuleFailure(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SourcePath = source["SourcePath"];
	        this.Tracker = source["Tracker"];
	        this.Rule = source["Rule"];
	        this.Reason = source["Reason"];
	        this.CreatedAt = this.convertValues(source["CreatedAt"], null);
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
	export class TrackerMetadata {
	    SourcePath: string;
	    Tracker: string;
	    TrackerID: string;
	    InfoHash: string;
	    TMDBID: number;
	    IMDBID: number;
	    TVDBID: number;
	    MALID: number;
	    Category: string;
	    Description: string;
	    ImageURLs: string[];
	    Filename: string;
	    Matched: boolean;
	    // Go type: time
	    UpdatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new TrackerMetadata(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SourcePath = source["SourcePath"];
	        this.Tracker = source["Tracker"];
	        this.TrackerID = source["TrackerID"];
	        this.InfoHash = source["InfoHash"];
	        this.TMDBID = source["TMDBID"];
	        this.IMDBID = source["IMDBID"];
	        this.TVDBID = source["TVDBID"];
	        this.MALID = source["MALID"];
	        this.Category = source["Category"];
	        this.Description = source["Description"];
	        this.ImageURLs = source["ImageURLs"];
	        this.Filename = source["Filename"];
	        this.Matched = source["Matched"];
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
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
	export class PlaylistSelection {
	    SourcePath: string;
	    SelectedPlaylists: string[];
	    UseAll: boolean;
	    // Go type: time
	    UpdatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new PlaylistSelection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SourcePath = source["SourcePath"];
	        this.SelectedPlaylists = source["SelectedPlaylists"];
	        this.UseAll = source["UseAll"];
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
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
	export class ReleaseNameOverrides {
	    Category?: string;
	    Type?: string;
	    Source?: string;
	    Resolution?: string;
	    Tag?: string;
	    Service?: string;
	    Edition?: string;
	    Season?: string;
	    Episode?: string;
	    EpisodeTitle?: string;
	    ManualYear?: number;
	    ManualDate?: string;
	    UseSeasonEpisode?: boolean;
	    NoSeason?: boolean;
	    NoYear?: boolean;
	    NoAKA?: boolean;
	    NoTag?: boolean;
	    NoEdition?: boolean;
	    NoDub?: boolean;
	    NoDual?: boolean;
	    DualAudio?: boolean;
	    Region?: string;
	
	    static createFrom(source: any = {}) {
	        return new ReleaseNameOverrides(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Category = source["Category"];
	        this.Type = source["Type"];
	        this.Source = source["Source"];
	        this.Resolution = source["Resolution"];
	        this.Tag = source["Tag"];
	        this.Service = source["Service"];
	        this.Edition = source["Edition"];
	        this.Season = source["Season"];
	        this.Episode = source["Episode"];
	        this.EpisodeTitle = source["EpisodeTitle"];
	        this.ManualYear = source["ManualYear"];
	        this.ManualDate = source["ManualDate"];
	        this.UseSeasonEpisode = source["UseSeasonEpisode"];
	        this.NoSeason = source["NoSeason"];
	        this.NoYear = source["NoYear"];
	        this.NoAKA = source["NoAKA"];
	        this.NoTag = source["NoTag"];
	        this.NoEdition = source["NoEdition"];
	        this.NoDub = source["NoDub"];
	        this.NoDual = source["NoDual"];
	        this.DualAudio = source["DualAudio"];
	        this.Region = source["Region"];
	    }
	}
	export class HistoryOverview {
	    SourcePath: string;
	    ReleaseTitle: string;
	    ReleaseSource: string;
	    ReleaseResolution: string;
	    // Go type: time
	    MetadataUpdatedAt: any;
	    LatestUploadStatus: string;
	    // Go type: time
	    LatestUploadAt: any;
	    StatusLabel: string;
	    Metadata: FileMetadata;
	    ExternalIDs: ExternalIDs;
	    ExternalMetadata: ExternalMetadata;
	    ReleaseNameOverrides: ReleaseNameOverrides;
	    DescriptionOverride: DescriptionOverride;
	    DescriptionOverrides: DescriptionOverride[];
	    PlaylistSelection: PlaylistSelection;
	    TrackerMetadata: TrackerMetadata[];
	    TrackerRuleFailures: TrackerRuleFailure[];
	    Screenshots: Screenshot[];
	    FinalSelections: ScreenshotFinalSelection[];
	    UploadedImages: UploadedImageLink[];
	    UploadHistory: UploadRecord[];
	
	    static createFrom(source: any = {}) {
	        return new HistoryOverview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SourcePath = source["SourcePath"];
	        this.ReleaseTitle = source["ReleaseTitle"];
	        this.ReleaseSource = source["ReleaseSource"];
	        this.ReleaseResolution = source["ReleaseResolution"];
	        this.MetadataUpdatedAt = this.convertValues(source["MetadataUpdatedAt"], null);
	        this.LatestUploadStatus = source["LatestUploadStatus"];
	        this.LatestUploadAt = this.convertValues(source["LatestUploadAt"], null);
	        this.StatusLabel = source["StatusLabel"];
	        this.Metadata = this.convertValues(source["Metadata"], FileMetadata);
	        this.ExternalIDs = this.convertValues(source["ExternalIDs"], ExternalIDs);
	        this.ExternalMetadata = this.convertValues(source["ExternalMetadata"], ExternalMetadata);
	        this.ReleaseNameOverrides = this.convertValues(source["ReleaseNameOverrides"], ReleaseNameOverrides);
	        this.DescriptionOverride = this.convertValues(source["DescriptionOverride"], DescriptionOverride);
	        this.DescriptionOverrides = this.convertValues(source["DescriptionOverrides"], DescriptionOverride);
	        this.PlaylistSelection = this.convertValues(source["PlaylistSelection"], PlaylistSelection);
	        this.TrackerMetadata = this.convertValues(source["TrackerMetadata"], TrackerMetadata);
	        this.TrackerRuleFailures = this.convertValues(source["TrackerRuleFailures"], TrackerRuleFailure);
	        this.Screenshots = this.convertValues(source["Screenshots"], Screenshot);
	        this.FinalSelections = this.convertValues(source["FinalSelections"], ScreenshotFinalSelection);
	        this.UploadedImages = this.convertValues(source["UploadedImages"], UploadedImageLink);
	        this.UploadHistory = this.convertValues(source["UploadHistory"], UploadRecord);
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
	
	
	
	
	
	
	
	
	export class TrackerPreview {
	    Tracker: string;
	    TrackerID: string;
	    TorrentURL: string;
	    InfoHash: string;
	    TMDBID: number;
	    IMDBID: number;
	    TVDBID: number;
	    MALID: number;
	    Category: string;
	    Description: string;
	    DescriptionHTML: string;
	    ImageURLs: string[];
	    Filename: string;
	    Matched: boolean;
	    UpdatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new TrackerPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Tracker = source["Tracker"];
	        this.TrackerID = source["TrackerID"];
	        this.TorrentURL = source["TorrentURL"];
	        this.InfoHash = source["InfoHash"];
	        this.TMDBID = source["TMDBID"];
	        this.IMDBID = source["IMDBID"];
	        this.TVDBID = source["TVDBID"];
	        this.MALID = source["MALID"];
	        this.Category = source["Category"];
	        this.Description = source["Description"];
	        this.DescriptionHTML = source["DescriptionHTML"];
	        this.ImageURLs = source["ImageURLs"];
	        this.Filename = source["Filename"];
	        this.Matched = source["Matched"];
	        this.UpdatedAt = source["UpdatedAt"];
	    }
	}
	export class MetadataPreview {
	    SourcePath: string;
	    TrackerName: string;
	    ReleaseName: string;
	    Warnings: string[];
	    ReleaseNameOverrides: ReleaseNameOverrides;
	    ExternalIDs: ExternalIDs;
	    ExternalIDCandidates: ExternalIDCandidates;
	    ExternalIDInfo: ExternalIDInfo[];
	    ExternalPreview: ExternalPreview[];
	    TrackerData: TrackerPreview[];
	
	    static createFrom(source: any = {}) {
	        return new MetadataPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SourcePath = source["SourcePath"];
	        this.TrackerName = source["TrackerName"];
	        this.ReleaseName = source["ReleaseName"];
	        this.Warnings = source["Warnings"];
	        this.ReleaseNameOverrides = this.convertValues(source["ReleaseNameOverrides"], ReleaseNameOverrides);
	        this.ExternalIDs = this.convertValues(source["ExternalIDs"], ExternalIDs);
	        this.ExternalIDCandidates = this.convertValues(source["ExternalIDCandidates"], ExternalIDCandidates);
	        this.ExternalIDInfo = this.convertValues(source["ExternalIDInfo"], ExternalIDInfo);
	        this.ExternalPreview = this.convertValues(source["ExternalPreview"], ExternalPreview);
	        this.TrackerData = this.convertValues(source["TrackerData"], TrackerPreview);
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
	export class PlaylistItem {
	    file: string;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new PlaylistItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.file = source["file"];
	        this.size = source["size"];
	    }
	}
	export class PlaylistInfo {
	    file: string;
	    duration: number;
	    items: PlaylistItem[];
	    score: number;
	    edition: string;
	
	    static createFrom(source: any = {}) {
	        return new PlaylistInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.file = source["file"];
	        this.duration = source["duration"];
	        this.items = this.convertValues(source["items"], PlaylistItem);
	        this.score = source["score"];
	        this.edition = source["edition"];
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
	
	
	export class PreparationDescription {
	    GroupKey: string;
	    Trackers: string[];
	    RawDescription: string;
	    RawDescriptionHTML: string;
	    Description: string;
	    DescriptionHTML: string;
	    HasOverride: boolean;
	    ImageHost: ImageHostFeedback;
	
	    static createFrom(source: any = {}) {
	        return new PreparationDescription(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.GroupKey = source["GroupKey"];
	        this.Trackers = source["Trackers"];
	        this.RawDescription = source["RawDescription"];
	        this.RawDescriptionHTML = source["RawDescriptionHTML"];
	        this.Description = source["Description"];
	        this.DescriptionHTML = source["DescriptionHTML"];
	        this.HasOverride = source["HasOverride"];
	        this.ImageHost = this.convertValues(source["ImageHost"], ImageHostFeedback);
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
	export class PreparationPreview {
	    SourcePath: string;
	    Descriptions: PreparationDescription[];
	
	    static createFrom(source: any = {}) {
	        return new PreparationPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SourcePath = source["SourcePath"];
	        this.Descriptions = this.convertValues(source["Descriptions"], PreparationDescription);
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
	
	
	export class ScreenshotError {
	    Index: number;
	    Message: string;
	
	    static createFrom(source: any = {}) {
	        return new ScreenshotError(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Index = source["Index"];
	        this.Message = source["Message"];
	    }
	}
	
	export class ScreenshotImage {
	    Index: number;
	    TimestampSeconds: number;
	    Path: string;
	    Width: number;
	    Height: number;
	    SizeBytes: number;
	    Host?: string;
	    ImgURL?: string;
	    RawURL?: string;
	    WebURL?: string;
	    // Go type: time
	    UploadedAt?: any;
	
	    static createFrom(source: any = {}) {
	        return new ScreenshotImage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Index = source["Index"];
	        this.TimestampSeconds = source["TimestampSeconds"];
	        this.Path = source["Path"];
	        this.Width = source["Width"];
	        this.Height = source["Height"];
	        this.SizeBytes = source["SizeBytes"];
	        this.Host = source["Host"];
	        this.ImgURL = source["ImgURL"];
	        this.RawURL = source["RawURL"];
	        this.WebURL = source["WebURL"];
	        this.UploadedAt = this.convertValues(source["UploadedAt"], null);
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
	export class ScreenshotLinkedImage {
	    Tracker: string;
	    URL: string;
	    Path: string;
	    Host: string;
	
	    static createFrom(source: any = {}) {
	        return new ScreenshotLinkedImage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Tracker = source["Tracker"];
	        this.URL = source["URL"];
	        this.Path = source["Path"];
	        this.Host = source["Host"];
	    }
	}
	export class ScreenshotSelection {
	    Index: number;
	    TimestampSeconds: number;
	    Frame: number;
	    Source: string;
	
	    static createFrom(source: any = {}) {
	        return new ScreenshotSelection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Index = source["Index"];
	        this.TimestampSeconds = source["TimestampSeconds"];
	        this.Frame = source["Frame"];
	        this.Source = source["Source"];
	    }
	}
	export class ScreenshotPlan {
	    SourcePath: string;
	    DiscType: string;
	    DurationSeconds: number;
	    FrameRate: number;
	    SuggestedSelections: ScreenshotSelection[];
	    ExistingScreenshots: ScreenshotImage[];
	    ExistingTrackerScreenshots: ScreenshotImage[];
	    FinalSelections: ScreenshotImage[];
	    TrackerImageLinks: ScreenshotLinkedImage[];
	    PreviewImages: ScreenshotImage[];
	    MetadataTimestamp: string;
	    RequiresManualFrames: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ScreenshotPlan(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SourcePath = source["SourcePath"];
	        this.DiscType = source["DiscType"];
	        this.DurationSeconds = source["DurationSeconds"];
	        this.FrameRate = source["FrameRate"];
	        this.SuggestedSelections = this.convertValues(source["SuggestedSelections"], ScreenshotSelection);
	        this.ExistingScreenshots = this.convertValues(source["ExistingScreenshots"], ScreenshotImage);
	        this.ExistingTrackerScreenshots = this.convertValues(source["ExistingTrackerScreenshots"], ScreenshotImage);
	        this.FinalSelections = this.convertValues(source["FinalSelections"], ScreenshotImage);
	        this.TrackerImageLinks = this.convertValues(source["TrackerImageLinks"], ScreenshotLinkedImage);
	        this.PreviewImages = this.convertValues(source["PreviewImages"], ScreenshotImage);
	        this.MetadataTimestamp = source["MetadataTimestamp"];
	        this.RequiresManualFrames = source["RequiresManualFrames"];
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
	export class ScreenshotResult {
	    SourcePath: string;
	    Purpose: string;
	    Images: ScreenshotImage[];
	    Tonemapped: boolean;
	    UsedLibplacebo: boolean;
	    Errors: ScreenshotError[];
	
	    static createFrom(source: any = {}) {
	        return new ScreenshotResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SourcePath = source["SourcePath"];
	        this.Purpose = source["Purpose"];
	        this.Images = this.convertValues(source["Images"], ScreenshotImage);
	        this.Tonemapped = source["Tonemapped"];
	        this.UsedLibplacebo = source["UsedLibplacebo"];
	        this.Errors = this.convertValues(source["Errors"], ScreenshotError);
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
	
	
	
	
	
	
	
	export class TrackerQuestionnaireField {
	    Key: string;
	    Label: string;
	    Kind: string;
	    Options: string[];
	    Value: string;
	    Placeholder: string;
	    Help: string;
	    Required: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TrackerQuestionnaireField(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Key = source["Key"];
	        this.Label = source["Label"];
	        this.Kind = source["Kind"];
	        this.Options = source["Options"];
	        this.Value = source["Value"];
	        this.Placeholder = source["Placeholder"];
	        this.Help = source["Help"];
	        this.Required = source["Required"];
	    }
	}
	export class TrackerQuestionnaire {
	    Tracker: string;
	    Fields: TrackerQuestionnaireField[];
	
	    static createFrom(source: any = {}) {
	        return new TrackerQuestionnaire(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Tracker = source["Tracker"];
	        this.Fields = this.convertValues(source["Fields"], TrackerQuestionnaireField);
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
	export class TrackerDryRunFile {
	    Field: string;
	    Path: string;
	    Present: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TrackerDryRunFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Field = source["Field"];
	        this.Path = source["Path"];
	        this.Present = source["Present"];
	    }
	}
	export class TrackerDryRunEntry {
	    Tracker: string;
	    Status: string;
	    Message: string;
	    ReleaseName: string;
	    DescriptionGroup: string;
	    Description: string;
	    Endpoint: string;
	    Payload: Record<string, string>;
	    Files: TrackerDryRunFile[];
	    Questionnaire?: TrackerQuestionnaire;
	    ImageHost: ImageHostFeedback;
	
	    static createFrom(source: any = {}) {
	        return new TrackerDryRunEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Tracker = source["Tracker"];
	        this.Status = source["Status"];
	        this.Message = source["Message"];
	        this.ReleaseName = source["ReleaseName"];
	        this.DescriptionGroup = source["DescriptionGroup"];
	        this.Description = source["Description"];
	        this.Endpoint = source["Endpoint"];
	        this.Payload = source["Payload"];
	        this.Files = this.convertValues(source["Files"], TrackerDryRunFile);
	        this.Questionnaire = this.convertValues(source["Questionnaire"], TrackerQuestionnaire);
	        this.ImageHost = this.convertValues(source["ImageHost"], ImageHostFeedback);
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
	
	export class TrackerDryRunPreview {
	    SourcePath: string;
	    Trackers: TrackerDryRunEntry[];
	
	    static createFrom(source: any = {}) {
	        return new TrackerDryRunPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SourcePath = source["SourcePath"];
	        this.Trackers = this.convertValues(source["Trackers"], TrackerDryRunEntry);
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

export namespace guiapp {
	
	export class DupeCheckTrackerState {
	    tracker: string;
	    status: string;
	    message: string;
	    result: api.DupeCheckResult;
	    startedAt: string;
	    finishedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new DupeCheckTrackerState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tracker = source["tracker"];
	        this.status = source["status"];
	        this.message = source["message"];
	        this.result = this.convertValues(source["result"], api.DupeCheckResult);
	        this.startedAt = source["startedAt"];
	        this.finishedAt = source["finishedAt"];
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
	export class DupeCheckSnapshot {
	    jobID: string;
	    sourcePath: string;
	    status: string;
	    trackers: DupeCheckTrackerState[];
	    completedCount: number;
	    totalCount: number;
	    summary: api.DupeCheckSummary;
	    error: string;
	    startedAt: string;
	    finishedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new DupeCheckSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.jobID = source["jobID"];
	        this.sourcePath = source["sourcePath"];
	        this.status = source["status"];
	        this.trackers = this.convertValues(source["trackers"], DupeCheckTrackerState);
	        this.completedCount = source["completedCount"];
	        this.totalCount = source["totalCount"];
	        this.summary = this.convertValues(source["summary"], api.DupeCheckSummary);
	        this.error = source["error"];
	        this.startedAt = source["startedAt"];
	        this.finishedAt = source["finishedAt"];
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
	
	export class ImportResult {
	    message: string;
	    warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new ImportResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.message = source["message"];
	        this.warnings = source["warnings"];
	    }
	}
	export class TrackerUploadTrackerState {
	    tracker: string;
	    status: string;
	    message: string;
	    uploadedCount: number;
	    startedAt: string;
	    finishedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new TrackerUploadTrackerState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tracker = source["tracker"];
	        this.status = source["status"];
	        this.message = source["message"];
	        this.uploadedCount = source["uploadedCount"];
	        this.startedAt = source["startedAt"];
	        this.finishedAt = source["finishedAt"];
	    }
	}
	export class TrackerUploadSnapshot {
	    jobID: string;
	    sourcePath: string;
	    status: string;
	    trackers: TrackerUploadTrackerState[];
	    failedTrackers: string[];
	    uploadedCount: number;
	    error: string;
	    startedAt: string;
	    finishedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new TrackerUploadSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.jobID = source["jobID"];
	        this.sourcePath = source["sourcePath"];
	        this.status = source["status"];
	        this.trackers = this.convertValues(source["trackers"], TrackerUploadTrackerState);
	        this.failedTrackers = source["failedTrackers"];
	        this.uploadedCount = source["uploadedCount"];
	        this.error = source["error"];
	        this.startedAt = source["startedAt"];
	        this.finishedAt = source["finishedAt"];
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

export namespace logging {
	
	export class Entry {
	    id: number;
	    // Go type: time
	    time: any;
	    level: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new Entry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.time = this.convertValues(source["time"], null);
	        this.level = source["level"];
	        this.message = source["message"];
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

