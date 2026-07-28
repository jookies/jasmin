import { Edit, useForm } from "@refinedev/antd";
import { RouteFields } from "./form";

export const RouteEdit = () => {
  const { formProps, saveButtonProps } = useForm();
  return (
    <Edit saveButtonProps={saveButtonProps}>
      <RouteFields formProps={formProps} editing />
    </Edit>
  );
};
