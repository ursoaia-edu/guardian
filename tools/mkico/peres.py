# Enumerate the resource directory of a PE file, so we know which id the
# window code must ask for.
import struct
import sys

RT = {1: "CURSOR", 2: "BITMAP", 3: "ICON", 4: "MENU", 5: "DIALOG", 6: "STRING",
      9: "ACCELERATOR", 10: "RCDATA", 11: "MESSAGETABLE", 12: "GROUP_CURSOR",
      14: "GROUP_ICON", 16: "VERSION", 24: "MANIFEST"}


def main(path):
    d = open(path, "rb").read()

    pe = struct.unpack_from("<I", d, 0x3C)[0]
    nsections = struct.unpack_from("<H", d, pe + 6)[0]
    optsize = struct.unpack_from("<H", d, pe + 20)[0]
    sect_off = pe + 24 + optsize

    rsrc_va = rsrc_raw = None
    for i in range(nsections):
        off = sect_off + 40 * i
        name = d[off:off + 8].rstrip(b"\x00").decode("ascii", "replace")
        va = struct.unpack_from("<I", d, off + 12)[0]
        raw = struct.unpack_from("<I", d, off + 20)[0]
        if name == ".rsrc":
            rsrc_va, rsrc_raw = va, raw

    if rsrc_va is None:
        print("no .rsrc section")
        return

    def walk(dir_off, level, parent):
        base = rsrc_raw + dir_off
        named, ids = struct.unpack_from("<HH", d, base + 12)
        for i in range(named + ids):
            e = base + 16 + 8 * i
            name_id, data_off = struct.unpack_from("<II", d, e)
            is_dir = data_off & 0x80000000
            data_off &= 0x7FFFFFFF

            if name_id & 0x80000000:
                label = "<named>"
            else:
                label = str(name_id)
                if level == 0:
                    label = "%s (%s)" % (name_id, RT.get(name_id, "?"))

            if is_dir:
                walk(data_off, level + 1, parent + [label])
            else:
                va, size = struct.unpack_from("<II", d, rsrc_raw + data_off)
                print("  type=%-16s id=%-6s lang=%-5s %7d bytes"
                      % (parent[0] if parent else "?",
                         parent[1] if len(parent) > 1 else "?",
                         label, size))

    walk(0, 0, [])


if __name__ == "__main__":
    main(sys.argv[1])
