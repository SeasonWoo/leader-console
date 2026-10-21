import Link from "next/link";

export default function NotFound() {
  return (
    <div className="flex flex-col items-center py-24 text-center">
      <p className="text-[15px] font-medium text-fg-2">页面不存在</p>
      <Link href="/" className="mt-3 text-[13px] text-accent hover:underline">
        回到待我决定
      </Link>
    </div>
  );
}
