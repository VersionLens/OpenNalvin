import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";

type ReasoningPartProps = {
  text: string;
  id?: string;
};

export function ReasoningPart({ text, id }: ReasoningPartProps) {
  return (
    <Accordion type="single" collapsible>
      <AccordionItem value={`reasoning-${id ?? "0"}`} className="border-white/8">
        <AccordionTrigger className="py-2 text-xs uppercase tracking-[0.16em] text-muted-foreground">
          Reasoning
        </AccordionTrigger>
        <AccordionContent>
          <pre className="overflow-x-auto whitespace-pre-wrap rounded-none border border-white/8 bg-black/25 p-3 text-xs text-muted-foreground">
            {text}
          </pre>
        </AccordionContent>
      </AccordionItem>
    </Accordion>
  );
}
