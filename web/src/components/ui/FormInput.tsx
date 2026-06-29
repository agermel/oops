import React from "react";

interface FormInputProps extends React.InputHTMLAttributes<HTMLInputElement | HTMLTextAreaElement> {
  multiline?: boolean;
  monospace?: boolean;
}

export function FormInput({
  multiline = false,
  monospace = false,
  className = "",
  ...rest
}: FormInputProps) {
  const cls = [
    "form-input",
    multiline && "form-textarea",
    monospace && "form-monospace",
    className,
  ]
    .filter(Boolean)
    .join(" ");

  if (multiline) {
    return <textarea className={cls} rows={3} {...rest} />;
  }

  return <input className={cls} {...rest} />;
}
