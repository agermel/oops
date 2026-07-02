import React from "react";

interface FormInputProps extends React.InputHTMLAttributes<HTMLInputElement | HTMLTextAreaElement> {
  multiline?: boolean;
  monospace?: boolean;
}

export const FormInput = React.forwardRef<HTMLInputElement | HTMLTextAreaElement, FormInputProps>(function FormInput({
  multiline = false,
  monospace = false,
  className = "",
  ...rest
}, ref) {
  const cls = [
    "form-input",
    multiline && "form-textarea",
    monospace && "form-monospace",
    className,
  ]
    .filter(Boolean)
    .join(" ");

  if (multiline) {
    return <textarea className={cls} rows={3} {...rest} ref={ref as React.Ref<HTMLTextAreaElement>} />;
  }

  return <input className={cls} {...rest} ref={ref as React.Ref<HTMLInputElement>} />;
});
