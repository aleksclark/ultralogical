import type { ButtonHTMLAttributes } from "react";
export function Button(props: ButtonHTMLAttributes<HTMLButtonElement>) { return <button {...props} className={`inline-flex items-center justify-center rounded-md bg-zinc-100 px-4 py-2 text-sm text-zinc-950 hover:bg-zinc-300 disabled:opacity-50 ${props.className ?? ""}`} />; }
