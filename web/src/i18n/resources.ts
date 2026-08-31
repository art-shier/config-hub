import { auth } from "./resources/auth";
import { common } from "./resources/common";
import { config } from "./resources/config";
import { machineAccess } from "./resources/machineAccess";
import { members } from "./resources/members";
import { projects } from "./resources/projects";
import { system } from "./resources/system";
import { versions } from "./resources/versions";

export const resources = {
  "en-US": {
    common: common["en-US"],
    auth: auth["en-US"],
    projects: projects["en-US"],
    config: config["en-US"],
    versions: versions["en-US"],
    members: members["en-US"],
    machineAccess: machineAccess["en-US"],
    system: system["en-US"],
  },
  "zh-CN": {
    common: common["zh-CN"],
    auth: auth["zh-CN"],
    projects: projects["zh-CN"],
    config: config["zh-CN"],
    versions: versions["zh-CN"],
    members: members["zh-CN"],
    machineAccess: machineAccess["zh-CN"],
    system: system["zh-CN"],
  },
} as const;
