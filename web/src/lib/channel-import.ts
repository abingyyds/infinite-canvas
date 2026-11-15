// Run before the router and analytics capture the initial URL.
function readChannelImport() {
    if (typeof window === "undefined") return null;
    const url = new URL(window.location.href);
    let baseUrl: string | undefined;
    let discardedKey = false;
    for (const [name, value] of [...url.searchParams]) {
        if (name.toLowerCase() === "baseurl") {
            baseUrl ||= value;
            url.searchParams.delete(name);
        } else if (name.toLowerCase() === "apikey") {
            discardedKey = true;
            url.searchParams.delete(name);
        }
    }
    if (!baseUrl && !discardedKey && url.href === window.location.href) return null;
    window.history.replaceState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`);
    return { baseUrl, discardedKey };
}

let pendingChannelImport = readChannelImport();

export function takeChannelImport() {
    const result = pendingChannelImport;
    pendingChannelImport = null;
    return result;
}
