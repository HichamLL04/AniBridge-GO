export function externalProviderUrl(
    provider: string,
    entryId: string,
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    _scope?: string | null,
): string | null {
    if (!entryId) return null;
    switch (provider) {
        case "anilist":
            return `https://anilist.co/anime/${entryId}`;
        case "anidb":
            return `https://anidb.net/anime/${entryId}`;
        case "imdb_movie":
        case "imdb_show":
            return `https://www.imdb.com/title/${entryId}`;
        case "tmdb_movie":
            return `https://www.themoviedb.org/movie/${entryId}`;
        case "tmdb_show":
            return `https://www.themoviedb.org/tv/${entryId}`;
        case "tvdb_movie":
            return `https://www.thetvdb.com/dereferrer/movie/${entryId}`;
        case "tvdb_show":
            return `https://www.thetvdb.com/dereferrer/series/${entryId}`;
        case "mal":
            return `https://myanimelist.net/anime/${entryId}`;
        default:
            return null;
    }
}
