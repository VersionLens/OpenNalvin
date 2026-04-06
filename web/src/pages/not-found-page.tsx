import { Link } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export function NotFoundPage() {
  return (
    <div className="grid min-h-[60vh] place-items-center">
      <Card className="max-w-xl text-center">
        <CardHeader>
          <CardTitle className="text-5xl">404</CardTitle>
          <CardDescription>This route is not wired yet, but the shell is ready for it.</CardDescription>
        </CardHeader>
        <CardContent>
          <Button asChild>
            <Link to="/">Return to overview</Link>
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}

