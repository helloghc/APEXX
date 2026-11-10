import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { PanelLeft } from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "./button";
import { Sheet, SheetContent, SheetDescription, SheetTitle } from "./sheet";

/**
 * Sidebar follows shadcn's API (SidebarProvider, Sidebar, SidebarHeader,
 * SidebarContent, SidebarMenu, SidebarMenuButton, SidebarFooter,
 * SidebarTrigger, SidebarInset) in a lighter form: a fixed column on wide
 * screens and a Sheet on phones.
 */
interface Ctx {
  openMobile: boolean;
  setOpenMobile: (v: boolean) => void;
}
const SidebarContext = React.createContext<Ctx | null>(null);

function useSidebar() {
  const c = React.useContext(SidebarContext);
  if (!c) throw new Error("useSidebar must be used inside SidebarProvider");
  return c;
}

function SidebarProvider({ className, children }: { className?: string; children: React.ReactNode }) {
  const [openMobile, setOpenMobile] = React.useState(false);
  return (
    <SidebarContext.Provider value={{ openMobile, setOpenMobile }}>
      <div className={cn("flex h-full w-full", className)}>{children}</div>
    </SidebarContext.Provider>
  );
}

function Sidebar({ className, children, label = "Menú" }: { className?: string; children: React.ReactNode; label?: string }) {
  const { openMobile, setOpenMobile } = useSidebar();
  return (
    <>
      <aside
        aria-label={label}
        className={cn("hidden w-60 shrink-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground md:flex", className)}
      >
        {children}
      </aside>
      <Sheet open={openMobile} onOpenChange={setOpenMobile}>
        <SheetContent side="left" className="w-[min(288px,85vw)] gap-0 p-0 md:hidden">
          <SheetTitle className="sr-only">{label}</SheetTitle>
          <SheetDescription className="sr-only">Navegación del panel</SheetDescription>
          <div className="flex h-full flex-col" onClick={(e) => (e.target as HTMLElement).closest("a") && setOpenMobile(false)}>
            {children}
          </div>
        </SheetContent>
      </Sheet>
    </>
  );
}

const SidebarHeader = ({ className, ...p }: React.HTMLAttributes<HTMLDivElement>) => <div className={cn("flex flex-col gap-1 p-4", className)} {...p} />;
const SidebarContent = ({ className, ...p }: React.HTMLAttributes<HTMLDivElement>) => (
  <div className={cn("flex min-h-0 flex-1 flex-col gap-2 overflow-auto px-2", className)} {...p} />
);
const SidebarFooter = ({ className, ...p }: React.HTMLAttributes<HTMLDivElement>) => (
  <div className={cn("flex flex-col gap-2 border-t border-sidebar-border p-4", className)} {...p} />
);
const SidebarGroupLabel = ({ className, ...p }: React.HTMLAttributes<HTMLDivElement>) => (
  <div className={cn("px-2 pt-2 text-xs font-medium text-muted-foreground", className)} {...p} />
);
const SidebarMenu = ({ className, ...p }: React.HTMLAttributes<HTMLUListElement>) => <ul className={cn("flex flex-col gap-0.5", className)} {...p} />;
const SidebarMenuItem = ({ className, ...p }: React.LiHTMLAttributes<HTMLLIElement>) => <li className={cn("relative", className)} {...p} />;

const SidebarMenuButton = React.forwardRef<
  HTMLButtonElement,
  React.ButtonHTMLAttributes<HTMLButtonElement> & { asChild?: boolean; isActive?: boolean }
>(({ asChild, isActive, className, ...props }, ref) => {
  const Comp = asChild ? Slot : "button";
  return (
    <Comp
      ref={ref}
      data-active={isActive || undefined}
      aria-current={isActive ? "page" : undefined}
      className={cn(
        "flex w-full items-center gap-2 rounded-md px-2.5 py-2 text-left text-sm text-sidebar-foreground outline-none transition-colors hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring data-[active]:bg-sidebar-accent data-[active]:font-semibold data-[active]:text-sidebar-accent-foreground [&_svg]:size-4 [&_svg]:shrink-0",
        className,
      )}
      {...props}
    />
  );
});
SidebarMenuButton.displayName = "SidebarMenuButton";

function SidebarTrigger({ className }: { className?: string }) {
  const { setOpenMobile } = useSidebar();
  return (
    <Button variant="ghost" size="icon" className={cn("md:hidden", className)} onClick={() => setOpenMobile(true)} aria-label="Abrir menú">
      <PanelLeft />
    </Button>
  );
}

const SidebarInset = ({ className, ...p }: React.HTMLAttributes<HTMLDivElement>) => (
  <div className={cn("flex min-h-0 min-w-0 flex-1 flex-col", className)} {...p} />
);

export {
  SidebarProvider,
  Sidebar,
  SidebarHeader,
  SidebarContent,
  SidebarFooter,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuItem,
  SidebarMenuButton,
  SidebarTrigger,
  SidebarInset,
  useSidebar,
};
