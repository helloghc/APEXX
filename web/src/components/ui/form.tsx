import * as React from "react";
import { cn } from "@/lib/utils";
import { Label } from "./label";

/**
 * Form helpers in the shape of shadcn's Form (FormItem / FormLabel /
 * FormDescription / FormMessage) without react-hook-form: the control gets
 * aria-describedby and aria-invalid wired from the field id.
 */
interface FieldProps {
  id: string;
  label: string;
  description?: React.ReactNode;
  error?: string;
  required?: boolean;
  className?: string;
  children: React.ReactElement<{ id?: string; "aria-invalid"?: boolean; "aria-describedby"?: string }>;
}

function FormField({ id, label, description, error, required, className, children }: FieldProps) {
  const describedBy = [description && !error ? `${id}-desc` : null, error ? `${id}-err` : null].filter(Boolean).join(" ") || undefined;
  return (
    <div className={cn("grid gap-1.5", className)}>
      <Label htmlFor={id}>
        {label}
        {required && (
          <span className="text-destructive" aria-hidden="true">
            {" "}
            *
          </span>
        )}
      </Label>
      {React.cloneElement(children, { id, "aria-invalid": error ? true : undefined, "aria-describedby": describedBy })}
      {description && !error && (
        <p id={`${id}-desc`} className="text-xs text-muted-foreground">
          {description}
        </p>
      )}
      {error && (
        <p id={`${id}-err`} className="text-xs font-medium text-destructive" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

export { FormField };
