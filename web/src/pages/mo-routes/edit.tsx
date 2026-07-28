import { Edit, useForm } from "@refinedev/antd";
import { MORouteFields } from "./form";

export const MORouteEdit = () => {
  const { formProps, saveButtonProps } = useForm();
  return (
    <Edit saveButtonProps={saveButtonProps}>
      <MORouteFields formProps={formProps} editing />
    </Edit>
  );
};
