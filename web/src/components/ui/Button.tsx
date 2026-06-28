import React from "react";

type ButtonVariant = "primary" | "ghost";
type ButtonSize = "xs" | "sm" | "md";

interface ButtonProps {
  variant?: ButtonVariant;
  size?: ButtonSize;
  danger?: boolean;
  disabled?: boolean;
  iconOnly?: boolean;
  className?: string;
  children: React.ReactNode;
  onClick?: (e: React.MouseEvent<HTMLButtonElement>) => void;
  title?: string;
  "aria-label"?: string;
  type?: "button" | "submit";
}

export function Button({
  variant = "primary",
  size = "md",
  danger = false,
  disabled = false,
  iconOnly = false,
  className = "",
  children,
  ...rest
}: ButtonProps) {
  const classes = [
    "btn",
    variant === "primary" ? "btn-primary" : "btn-ghost",
    size === "sm" && "btn-sm",
    size === "xs" && "btn-xs",
    danger && "btn-danger",
    iconOnly && "btn-icon",
    className,
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <button className={classes} disabled={disabled} {...rest}>
      {children}
    </button>
  );
}
