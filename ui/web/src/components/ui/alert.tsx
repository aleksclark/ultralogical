import type { HTMLAttributes } from "react";
import { cn } from "@/lib/utils";

export function Alert({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      role="alert"
      className={cn("rounded-lg border border-red-800 bg-red-950/80 p-3 text-sm text-red-100", className)}
      {...props}
    />
  );
}
