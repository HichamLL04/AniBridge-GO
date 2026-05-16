export function titleCase(str: string): string {
    return str
        .toLowerCase()
        .split(/[\s_-]+/)
        .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
        .join(" ");
}
